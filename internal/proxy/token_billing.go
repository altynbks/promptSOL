package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"unicode/utf8"

	"ai-solana-proxy/internal/solana"
	"ai-solana-proxy/internal/store"
)

const maxChatRequestBytes = 32 << 10
const maxCompletionTokens = 768

// The browser wallet SDK accepts lamports as a JavaScript number. Refuse a
// quote above this bound so the displayed and signed transfer stays exact.
const maxExactBrowserLamports = uint64(1<<53 - 1)

var errQuoteAmountNotExactInBrowser = errors.New("quoted amount exceeds the exact browser integer range")

type PaymentVerifier interface {
	VerifyTransaction(signature, recipient string, amount uint64) (bool, error)
}

type TokenBillingHandler struct {
	Upstream         http.Handler
	Validator        PaymentVerifier
	SignatureCache   *store.MemoryCache
	Pending          *store.PendingPayments
	Recipient        string
	LamportsPerToken uint64
}

type quoteResponse struct {
	Error            string `json:"error"`
	PaymentID        string `json:"payment_id,omitempty"`
	PayTo            string `json:"pay_to,omitempty"`
	Amount           uint64 `json:"amount,omitempty"`
	TokenCount       uint64 `json:"token_count,omitempty"`
	LamportsPerToken uint64 `json:"lamports_per_token,omitempty"`
	ExpiresInSeconds int    `json:"expires_in_seconds,omitempty"`
}

func (h *TokenBillingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if signature := r.Header.Get("X-Payment-Signature"); signature != "" {
		h.deliver(w, r, signature)
		return
	}
	h.createQuote(w, r)
}

func (h *TokenBillingHandler) createQuote(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChatRequestBytes))
	if err != nil || !json.Valid(body) {
		http.Error(w, "Request must be valid JSON under 32 KiB", http.StatusBadRequest)
		return
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil || request["messages"] == nil || request["model"] == nil {
		http.Error(w, "A model and messages are required", http.StatusBadRequest)
		return
	}
	request["stream"], _ = json.Marshal(false)
	request["max_tokens"], _ = json.Marshal(maxCompletionTokens)
	request["reasoning_effort"], _ = json.Marshal("low")
	body, err = json.Marshal(request)
	if err != nil {
		http.Error(w, "Invalid chat request", http.StatusBadRequest)
		return
	}
	upstreamRequest := r.Clone(r.Context())
	upstreamRequest.Body = io.NopCloser(bytes.NewReader(body))
	upstreamRequest.ContentLength = int64(len(body))
	upstreamRequest.Header = r.Header.Clone()
	upstreamRequest.Header.Del("X-Payment-Signature")
	upstreamRequest.Header.Del("X-Payment-ID")
	upstreamRequest.Header.Set("Content-Type", "application/json")
	// Let Go's transport negotiate gzip itself so it also transparently
	// decompresses the provider response before usage is parsed and cached.
	upstreamRequest.Header.Del("Accept-Encoding")

	result := httptest.NewRecorder()
	h.Upstream.ServeHTTP(result, upstreamRequest)
	if result.Code < 200 || result.Code >= 300 {
		http.Error(w, providerHTTPError(result.Code), http.StatusBadGateway)
		return
	}
	providerBody := normalizeProviderBody(result.Body.Bytes())
	tokens, responseError := parseProviderChatResponse(providerBody, result.Header().Get("Content-Type"))
	if responseError != "" {
		http.Error(w, responseError+" (HTTP "+strconv.Itoa(result.Code)+"). Платёж в Solana не создавался; AI-запрос мог тарифицироваться провайдером.", http.StatusBadGateway)
		return
	}
	if h.LamportsPerToken == 0 || tokens > ^uint64(0)/h.LamportsPerToken {
		http.Error(w, "Token quote is invalid; no payment was taken", http.StatusBadGateway)
		return
	}
	amount, err := QuoteAmount(tokens, h.LamportsPerToken)
	if err != nil {
		if errors.Is(err, errQuoteAmountNotExactInBrowser) {
			http.Error(w, "Стоимость превышает безопасный лимит точного расчёта демо; уменьшите ставку за токен.", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "Token quote is invalid", http.StatusBadGateway)
		return
	}
	quote, ok := h.Pending.Put(providerBody, tokens, amount)
	if !ok {
		http.Error(w, "Too many unpaid quotes are active; retry after a few minutes", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(quoteResponse{
		Error:            "Payment Required",
		PaymentID:        quote.ID,
		PayTo:            h.Recipient,
		Amount:           quote.Amount,
		TokenCount:       quote.Tokens,
		LamportsPerToken: h.LamportsPerToken,
		ExpiresInSeconds: int(store.PendingQuoteTTL.Seconds()),
	})
}

func providerHTTPError(status int) string {
	var cause string
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		cause = "Gemini отклонил доступ. Проверьте UPSTREAM_API_KEY и доступ ключа к Gemini API."
	case http.StatusNotFound:
		cause = "Gemini не нашёл endpoint или модель. Проверьте UPSTREAM_BASE_URL и AI_MODEL."
	case http.StatusTooManyRequests:
		cause = "Gemini сообщил о лимите или квоте. Проверьте квоту проекта и повторите позже."
	case http.StatusServiceUnavailable:
		cause = "Gemini временно перегружен или недоступен. Подождите несколько секунд; повтор отправит новый AI-запрос."
	default:
		if status >= 500 {
			cause = "У Gemini временная ошибка сервера. Повторите запрос позже."
		} else {
			cause = "Gemini отклонил запрос. Проверьте конфигурацию и совместимость запроса."
		}
	}
	return cause + " (HTTP " + strconv.Itoa(status) + "). Котировка не создана, Phantom не вызывался; запрос мог тарифицироваться провайдером."
}

type providerChatResponse struct {
	Usage              json.RawMessage `json:"usage"`
	UsageMetadata      json.RawMessage `json:"usageMetadata"`
	UsageMetadataSnake json.RawMessage `json:"usage_metadata"`
	Error              json.RawMessage `json:"error"`
	Choices            []struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// parseProviderChatResponse accepts the common OpenAI usage spellings and
// Gemini usage metadata spellings, while still requiring a usable chat answer.
// It intentionally never returns provider response text to the caller.
func parseProviderChatResponse(body []byte, contentType string) (uint64, string) {
	body = normalizeProviderBody(body)
	mediaType, _, _ := mime.ParseMediaType(contentType)
	mediaType = strings.ToLower(mediaType)
	if !json.Valid(body) {
		trimmed := bytes.TrimSpace(body)
		if mediaType == "text/event-stream" || bytes.HasPrefix(trimmed, []byte("data:")) {
			return 0, "Провайдер прислал потоковый ответ вместо JSON; оплата не отправлена. Проверьте поддержку stream=false."
		}
		if !isJSONMediaType(mediaType) {
			if mediaType == "" {
				mediaType = "неизвестного типа"
			}
			return 0, "Провайдер прислал не-JSON ответ (тип: " + mediaType + "); оплата не отправлена. Проверьте UPSTREAM_BASE_URL, модель и доступ API-ключа."
		}
		return 0, "Провайдер прислал повреждённый JSON (тип: " + mediaType + ", " + strconv.Itoa(len(body)) + " байт): " + invalidJSONDetail(body) + ". Оплата не отправлена. Проверьте endpoint и совместимость API."
	}

	var response providerChatResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, "Провайдер вернул JSON неподдерживаемого формата; оплата не отправлена. Проверьте совместимость chat/completions API."
	}
	if len(response.Error) > 0 && string(response.Error) != "null" {
		return 0, "Провайдер вернул ошибку вместо ответа; оплата не отправлена. Проверьте модель, endpoint и доступ API-ключа."
	}
	if len(response.Choices) == 0 || !isNonEmptyJSONString(response.Choices[0].Message.Content) {
		return 0, "Провайдер не вернул текстовый chat completion; оплата не отправлена. Проверьте модель и совместимость API."
	}

	for _, rawUsage := range []json.RawMessage{response.Usage, response.UsageMetadata, response.UsageMetadataSnake} {
		if tokens := parseUsageTokens(rawUsage); tokens > 0 {
			return tokens, ""
		}
	}
	return 0, "Провайдер вернул ответ, но фактическое число токенов не распознано; оплата не отправлена. Требуется usage.total_tokens или эквивалентное поле usage."
}

func normalizeProviderBody(body []byte) []byte {
	return bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
}

func invalidJSONDetail(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "тело ответа пустое"
	}
	lower := bytes.ToLower(trimmed)
	if bytes.HasPrefix(lower, []byte("<")) {
		return "тело похоже на HTML/XML или страницу промежуточного прокси, хотя указан JSON"
	}
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		return "тело похоже на поток SSE"
	}
	if !utf8.Valid(trimmed) {
		return "тело ответа не в UTF-8; ожидается UTF-8 JSON"
	}
	if !isJSONLeadingByte(trimmed[0]) {
		return "ответ начинается с обычного текста или неизвестного формата, а не с JSON"
	}
	var ignored json.RawMessage
	var syntaxError *json.SyntaxError
	if err := json.Unmarshal(body, &ignored); errors.As(err, &syntaxError) {
		if strings.Contains(syntaxError.Error(), "unexpected end of JSON input") {
			return "ответ JSON оборван примерно у байта " + strconv.FormatInt(syntaxError.Offset, 10)
		}
		return "синтаксическая ошибка JSON примерно у байта " + strconv.FormatInt(syntaxError.Offset, 10)
	}
	return "структура тела ответа не является корректным JSON"
}

func isJSONLeadingByte(value byte) bool {
	return value == '{' || value == '[' || value == '"' || value == '-' ||
		(value >= '0' && value <= '9') || value == 't' || value == 'f' || value == 'n'
}

func isJSONMediaType(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

func isNonEmptyJSONString(raw json.RawMessage) bool {
	var value string
	return len(raw) > 0 && json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func parseUsageTokens(raw json.RawMessage) uint64 {
	var fields map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &fields) != nil {
		return 0
	}
	if total := firstUsageCount(fields, "total_tokens", "totalTokens", "total_token_count", "totalTokenCount"); total > 0 {
		return total
	}
	input := firstUsageCount(fields, "prompt_tokens", "promptTokens", "input_tokens", "inputTokens", "prompt_token_count", "promptTokenCount")
	output := firstUsageCount(fields, "completion_tokens", "completionTokens", "output_tokens", "outputTokens", "candidates_token_count", "candidatesTokenCount", "response_token_count", "responseTokenCount")
	if input == 0 || output == 0 || input > ^uint64(0)-output {
		return 0
	}
	return input + output
}

func firstUsageCount(fields map[string]json.RawMessage, names ...string) uint64 {
	for _, name := range names {
		if count, ok := parseUsageCount(fields[name]); ok {
			return count
		}
	}
	return 0
}

func parseUsageCount(raw json.RawMessage) (uint64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	value := strings.TrimSpace(string(raw))
	if strings.HasPrefix(value, `"`) {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return 0, false
		}
		value = text
	}
	count, err := strconv.ParseUint(value, 10, 64)
	return count, err == nil
}

func (h *TokenBillingHandler) deliver(w http.ResponseWriter, r *http.Request, signature string) {
	id := r.Header.Get("X-Payment-ID")
	if id == "" {
		http.Error(w, "Missing payment ID", http.StatusPaymentRequired)
		return
	}
	if completed, ok := h.Pending.GetCompleted(id, signature); ok {
		writePaidResponse(w, completed.Body)
		return
	}
	quote, ok := h.Pending.Get(id)
	if !ok {
		http.Error(w, "Payment quote expired or not found; request a new quote", http.StatusPaymentRequired)
		return
	}
	valid, err := h.Validator.VerifyTransaction(signature, h.Recipient, quote.Amount)
	if err != nil {
		http.Error(w, "Solana payment verification is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if !valid {
		http.Error(w, "Payment is not confirmed for the quoted amount", http.StatusPaymentRequired)
		return
	}
	if !h.SignatureCache.Claim(signature) {
		http.Error(w, "This payment was already used", http.StatusPaymentRequired)
		return
	}
	completed, ok := h.Pending.Complete(id, signature)
	if !ok {
		http.Error(w, "This quote was already paid or has expired", http.StatusPaymentRequired)
		return
	}
	writePaidResponse(w, completed.Body)
}

func writePaidResponse(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func QuoteAmount(tokens, lamportsPerToken uint64) (uint64, error) {
	if tokens == 0 || lamportsPerToken == 0 {
		return 0, errors.New("token count and rate must be positive")
	}
	if tokens > ^uint64(0)/lamportsPerToken {
		return 0, errors.New("quoted payment amount overflows uint64")
	}
	amount := tokens * lamportsPerToken
	if amount > maxExactBrowserLamports {
		return 0, errQuoteAmountNotExactInBrowser
	}
	return amount, nil
}

var _ PaymentVerifier = (*solana.Validator)(nil)
