package proxy

import (
	"encoding/json"
	"net/http"

	"ai-solana-proxy/internal/solana"
	"ai-solana-proxy/internal/store"
)

type PaymentMiddleware struct {
	Validator    *solana.Validator
	Cache        *store.MemoryCache
	WalletPubKey string
	Price        uint64
}

func (m *PaymentMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sig := r.Header.Get("X-Payment-Signature")
		if sig == "" {
			m.requirePayment(w)
			return
		}
		valid, err := m.Validator.VerifyTransaction(sig, m.WalletPubKey, m.Price)
		if err != nil {
			http.Error(w, "Payment verification unavailable", http.StatusServiceUnavailable)
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
		next.ServeHTTP(w, r)
	})
}

func (m *PaymentMiddleware) requirePayment(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Solana-Pay-To", m.WalletPubKey)
	w.Header().Set("X-Payment-Amount", stringUint(m.Price))
	w.WriteHeader(http.StatusPaymentRequired)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "Payment Required", "pay_to": m.WalletPubKey, "amount": m.Price})
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
