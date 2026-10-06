package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"ai-solana-proxy/internal/solana"
	"ai-solana-proxy/internal/store"
)

type PaymentMiddleware struct {
	Validator        *solana.Validator
	Cache            *store.MemoryCache
	WalletPubKey     string
	Price            uint64
	InputTokenPrice  uint64
	OutputTokenPrice uint64
}

func (m *PaymentMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		price := m.Price
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions") {
			body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
			if err != nil || len(body) > 2<<20 {
				http.Error(w, "Request body is too large or could not be read", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			quote, err := EstimateQuote(body, m.InputTokenPrice, m.OutputTokenPrice, m.Price)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			price = quote.Amount
		}
		sig := r.Header.Get("X-Payment-Signature")
		if sig == "" {
			m.requirePayment(w, price)
			return
		}
		valid, err := m.Validator.VerifyTransaction(sig, m.WalletPubKey, price)
		if err != nil {
			log.Printf("payment verification failed: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Solana RPC is unavailable or timed out. Check SOLANA_RPC_URL and RPC_TIMEOUT_SECONDS in .env; see proxy logs for details."})
			return
		}
		if !valid {
			http.Error(w, "Invalid or unconfirmed transaction", http.StatusPaymentRequired)
			return
		}
		// Claim after successful verification. Claim is atomic, so concurrent retries
		// cannot both reach the paid endpoint.
		if !m.Cache.Claim(sig) {
			http.Error(w, "Transaction already used or payment cache full", http.StatusPaymentRequired)
			return
		}
		tracked := &responseStatusWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)
		if tracked.statusCode >= http.StatusBadRequest {
			m.Cache.Release(sig)
		}
	})
}

type responseStatusWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseStatusWriter) WriteHeader(statusCode int) {
	if w.statusCode != 0 {
		return
	}
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *responseStatusWriter) Write(data []byte) (int, error) {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *responseStatusWriter) Flush() {
	if w.statusCode == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (m *PaymentMiddleware) requirePayment(w http.ResponseWriter, price uint64) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Solana-Pay-To", m.WalletPubKey)
	w.Header().Set("X-Payment-Amount", stringUint(price))
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "Payment Required", "pay_to": m.WalletPubKey, "amount": price})
}

func stringUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
