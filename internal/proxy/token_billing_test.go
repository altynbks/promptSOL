package proxy

import (
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ai-solana-proxy/internal/store"
)

type testVerifier struct {
	valid      bool
	calls      int
	wantAmount uint64
}

func (v *testVerifier) VerifyTransaction(_ string, _ string, amount uint64) (bool, error) {
	v.calls++
	if v.wantAmount != 0 && amount != v.wantAmount {
		return false, nil
	}
	return v.valid, nil
}

func TestTokenBillingRequiresExactPaymentBeforeAnswer(t *testing.T) {
	upstreamCalls := 0
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["stream"] != false || request["max_tokens"] != float64(maxCompletionTokens) || request["reasoning_effort"] != "low" {
			t.Fatalf("upstream request was not bounded and non-streaming: %#v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"paid answer"}}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`))
	})
	verifier := &testVerifier{valid: true, wantAmount: 70}
	handler := &TokenBillingHandler{
		Upstream: upstream, Validator: verifier, SignatureCache: store.NewMemoryCache(20),
		Pending: store.NewPendingPayments(20), Recipient: "devnet-recipient", LamportsPerToken: 10,
	}
	payload := `{"model":"gemini-3.8-flash","messages":[{"role":"user","content":"hello"}],"stream":true}`

	quoteRecorder := httptest.NewRecorder()
	handler.ServeHTTP(quoteRecorder, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload)))
	if quoteRecorder.Code != http.StatusPaymentRequired {
		t.Fatalf("quote status = %d, want 402: %s", quoteRecorder.Code, quoteRecorder.Body.String())
	}
	if strings.Contains(quoteRecorder.Body.String(), "paid answer") {
		t.Fatal("answer was exposed before payment")
	}
	var quote quoteResponse
	if err := json.Unmarshal(quoteRecorder.Body.Bytes(), &quote); err != nil {
		t.Fatal(err)
	}
	if quote.Amount != 70 || quote.TokenCount != 7 || quote.PayTo != "devnet-recipient" || quote.PaymentID == "" {
		t.Fatalf("unexpected quote: %+v", quote)
	}

	paidRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	paidRequest.Header.Set("X-Payment-ID", quote.PaymentID)
	paidRequest.Header.Set("X-Payment-Signature", "devnet-signature")
	paidResponse := httptest.NewRecorder()
	handler.ServeHTTP(paidResponse, paidRequest)
	if paidResponse.Code != http.StatusOK || !strings.Contains(paidResponse.Body.String(), "paid answer") {
		t.Fatalf("paid response = %d %s", paidResponse.Code, paidResponse.Body.String())
	}
	if verifier.calls != 1 || upstreamCalls != 1 {
		t.Fatalf("verifier calls=%d upstream calls=%d, want one each", verifier.calls, upstreamCalls)
	}

	// Retrying delivery after a lost HTTP response returns the same paid result
	// without spending the signature again or calling the AI provider again.
	retryRequest := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	retryRequest.Header.Set("X-Payment-ID", quote.PaymentID)
	retryRequest.Header.Set("X-Payment-Signature", "devnet-signature")
	retryResponse := httptest.NewRecorder()
	handler.ServeHTTP(retryResponse, retryRequest)
	if retryResponse.Code != http.StatusOK || !strings.Contains(retryResponse.Body.String(), "paid answer") {
		t.Fatalf("retry response = %d %s", retryResponse.Code, retryResponse.Body.String())
	}
	if verifier.calls != 1 || upstreamCalls != 1 {
		t.Fatalf("retry repeated work: verifier calls=%d upstream calls=%d", verifier.calls, upstreamCalls)
	}
}

func TestTokenBillingRejectsInexactBrowserQuoteBeforePayment(t *testing.T) {
	handler := &TokenBillingHandler{
		Upstream: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"answer"}}],"usage":{"total_tokens":2}}`))
		}),
		Validator: &testVerifier{valid: true}, SignatureCache: store.NewMemoryCache(5), Pending: store.NewPendingPayments(5),
		Recipient: "recipient", LamportsPerToken: maxExactBrowserLamports/2 + 1,
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`)))
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "безопасный лимит точного расчёта") {
		t.Fatalf("inexact quote response = %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "payment_id") {
		t.Fatalf("inexact quote created a payment: %s", response.Body.String())
	}
}

func TestTokenBillingRejectsMissingUsageAndInvalidPayment(t *testing.T) {
	t.Run("provider HTTP errors explain why Phantom did not open", func(t *testing.T) {
		handler := &TokenBillingHandler{
			Upstream: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"private provider details"}}`))
			}),
			Validator: &testVerifier{valid: true}, SignatureCache: store.NewMemoryCache(5), Pending: store.NewPendingPayments(5),
			Recipient: "recipient", LamportsPerToken: 10,
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`)))
		body := response.Body.String()
		if response.Code != http.StatusBadGateway || !strings.Contains(body, "HTTP 401") || !strings.Contains(body, "UPSTREAM_API_KEY") || !strings.Contains(body, "Phantom не вызывался") {
			t.Fatalf("provider authorization failure = %d %s", response.Code, body)
		}
		if strings.Contains(body, "private provider details") || strings.Contains(body, "payment_id") {
			t.Fatalf("provider error body leaked or quote created: %s", body)
		}
	})

	t.Run("invalid provider body fails closed without exposing body", func(t *testing.T) {
		handler := &TokenBillingHandler{
			Upstream: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write([]byte(`<html>private provider response</html>`))
			}),
			Validator: &testVerifier{valid: true}, SignatureCache: store.NewMemoryCache(5), Pending: store.NewPendingPayments(5),
			Recipient: "recipient", LamportsPerToken: 10,
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`)))
		if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "не-JSON ответ (тип: text/html)") || !strings.Contains(response.Body.String(), "AI-запрос мог тарифицироваться провайдером") {
			t.Fatalf("invalid provider response = %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "private provider response") || strings.Contains(response.Body.String(), "payment_id") {
			t.Fatalf("invalid provider response leaked data or created a quote: %s", response.Body.String())
		}
	})

	t.Run("malformed JSON reports safe parser details", func(t *testing.T) {
		handler := &TokenBillingHandler{
			Upstream: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"private":"provider data"`))
			}),
			Validator: &testVerifier{valid: true}, SignatureCache: store.NewMemoryCache(5), Pending: store.NewPendingPayments(5),
			Recipient: "recipient", LamportsPerToken: 10,
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`)))
		body := response.Body.String()
		if response.Code != http.StatusBadGateway || !strings.Contains(body, "повреждённый JSON") || !strings.Contains(body, "HTTP 200") || !strings.Contains(body, "примерно у байта") {
			t.Fatalf("malformed provider response = %d %s", response.Code, body)
		}
		if strings.Contains(body, "provider data") || strings.Contains(body, "payment_id") {
			t.Fatalf("provider data leaked or quote created: %s", body)
		}
	})

	t.Run("missing actual usage fails closed", func(t *testing.T) {
		handler := &TokenBillingHandler{
			Upstream:  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"choices":[]}`)) }),
			Validator: &testVerifier{valid: true}, SignatureCache: store.NewMemoryCache(5), Pending: store.NewPendingPayments(5),
			Recipient: "recipient", LamportsPerToken: 10,
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[]}`)))
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "Payment Required") {
			t.Fatalf("missing usage response = %d %s", response.Code, response.Body.String())
		}
	})

	t.Run("unconfirmed payment does not release answer", func(t *testing.T) {
		verifier := &testVerifier{valid: false, wantAmount: 10}
		handler := &TokenBillingHandler{
			Upstream: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"secret answer"}}],"usage":{"total_tokens":1}}`))
			}),
			Validator: verifier, SignatureCache: store.NewMemoryCache(5), Pending: store.NewPendingPayments(5),
			Recipient: "recipient", LamportsPerToken: 10,
		}
		payload := `{"model":"m","messages":[]}`
		quote := httptest.NewRecorder()
		handler.ServeHTTP(quote, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload)))
		var payment quoteResponse
		if err := json.Unmarshal(quote.Body.Bytes(), &payment); err != nil {
			t.Fatal(err)
		}
		delivery := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
		delivery.Header.Set("X-Payment-ID", payment.PaymentID)
		delivery.Header.Set("X-Payment-Signature", "not-confirmed")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, delivery)
		if response.Code != http.StatusPaymentRequired || strings.Contains(response.Body.String(), "secret answer") {
			t.Fatalf("invalid payment response = %d %s", response.Code, response.Body.String())
		}
	})
}

func TestParseProviderChatResponseUsageFormats(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantTokens  uint64
		wantError   string
	}{
		{
			name:        "OpenAI snake case total",
			body:        `{"choices":[{"message":{"content":"answer"}}],"usage":{"total_tokens":7,"prompt_tokens":4,"completion_tokens":3}}`,
			contentType: "application/json",
			wantTokens:  7,
		},
		{
			name:        "UTF-8 BOM is tolerated",
			body:        "\uFEFF" + `{"choices":[{"message":{"content":"answer"}}],"usage":{"total_tokens":7}}`,
			contentType: "application/json",
			wantTokens:  7,
		},
		{
			name:        "OpenAI camel case split counts",
			body:        `{"choices":[{"message":{"content":"answer"}}],"usage":{"promptTokens":"4","completionTokens":"3"}}`,
			contentType: "application/json; charset=utf-8",
			wantTokens:  7,
		},
		{
			name:        "Gemini usage metadata",
			body:        `{"choices":[{"message":{"content":"answer"}}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":3,"totalTokenCount":7}}`,
			contentType: "application/json",
			wantTokens:  7,
		},
		{
			name:        "input output aliases",
			body:        `{"choices":[{"message":{"content":"answer"}}],"usage":{"input_tokens":4,"output_tokens":3}}`,
			contentType: "application/json",
			wantTokens:  7,
		},
		{
			name:        "non JSON response explains content type without body",
			body:        `<html>private provider response</html>`,
			contentType: "text/html; charset=utf-8",
			wantError:   "не-JSON ответ (тип: text/html)",
		},
		{
			name:        "streaming response is identified",
			body:        `data: {"choices":[]}`,
			contentType: "text/event-stream",
			wantError:   "потоковый ответ",
		},
		{
			name:        "malformed JSON does not echo body",
			body:        `not json private provider response`,
			contentType: "application/json",
			wantError:   "синтаксическая ошибка JSON примерно у байта",
		},
		{
			name:        "HTML mislabeled as JSON is identified safely",
			body:        `<html>private provider response</html>`,
			contentType: "application/json",
			wantError:   "тело похоже на HTML",
		},
		{
			name:        "truncated JSON reports position without content",
			body:        `{"choices":[`,
			contentType: "application/json",
			wantError:   "ответ JSON оборван примерно у байта",
		},
		{
			name:        "empty JSON response is identified",
			body:        " \n\t",
			contentType: "application/json",
			wantError:   "тело ответа пустое",
		},
		{
			name:        "missing text answer is rejected before payment",
			body:        `{"choices":[{"message":{"content":""}}],"usage":{"total_tokens":7}}`,
			contentType: "application/json",
			wantError:   "не вернул текстовый chat completion",
		},
		{
			name:        "missing usage is rejected before payment",
			body:        `{"choices":[{"message":{"content":"answer"}}]}`,
			contentType: "application/json",
			wantError:   "фактическое число токенов не распознано",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokens, responseError := parseProviderChatResponse([]byte(test.body), test.contentType)
			if tokens != test.wantTokens {
				t.Fatalf("token count = %d, want %d (error %q)", tokens, test.wantTokens, responseError)
			}
			if test.wantError == "" && responseError != "" {
				t.Fatalf("unexpected response error: %s", responseError)
			}
			if test.wantError != "" && !strings.Contains(responseError, test.wantError) {
				t.Fatalf("response error %q does not contain %q", responseError, test.wantError)
			}
			if strings.Contains(responseError, "private provider response") {
				t.Fatalf("provider body leaked in error: %s", responseError)
			}
		})
	}
}

func TestNewOpenAIProxyJoinsConfiguredBasePath(t *testing.T) {
	var gotPath, gotAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := NewOpenAIProxy(upstream.URL+"/v1beta/openai/", "test-api-key")
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	proxy.ServeHTTP(recorder, request)
	if gotPath != "/v1beta/openai/chat/completions" {
		t.Fatalf("upstream path = %q", gotPath)
	}
	if gotAuthorization != "Bearer test-api-key" {
		t.Fatalf("upstream authorization header was not set")
	}
}

func TestTokenBillingDecompressesGzipProviderResponse(t *testing.T) {
	var upstreamAcceptEncoding string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		compressed := gzip.NewWriter(w)
		_, _ = compressed.Write([]byte(`{"choices":[{"message":{"content":"answer"}}],"usage":{"total_tokens":4}}`))
		_ = compressed.Close()
	}))
	defer server.Close()

	upstream, err := NewOpenAIProxy(server.URL+"/v1beta/openai/", "test-api-key")
	if err != nil {
		t.Fatal(err)
	}
	handler := &TokenBillingHandler{
		Upstream: upstream, Validator: &testVerifier{valid: true}, SignatureCache: store.NewMemoryCache(5),
		Pending: store.NewPendingPayments(5), Recipient: "recipient", LamportsPerToken: 10,
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hello"}]}`))
	request.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPaymentRequired {
		t.Fatalf("gzip provider response = %d %s", response.Code, response.Body.String())
	}
	var quote quoteResponse
	if err := json.Unmarshal(response.Body.Bytes(), &quote); err != nil {
		t.Fatal(err)
	}
	if quote.TokenCount != 4 || quote.Amount != 40 {
		t.Fatalf("gzip response quote = %+v", quote)
	}
	if upstreamAcceptEncoding != "gzip" {
		t.Fatalf("upstream Accept-Encoding = %q, want transport-negotiated gzip", upstreamAcceptEncoding)
	}
}
