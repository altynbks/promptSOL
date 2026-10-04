package solana

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerifyTransactionRequiresExactQuotedAmount(t *testing.T) {
	for _, test := range []struct {
		name   string
		amount uint64
		want   bool
	}{
		{name: "exact quote", amount: 70, want: true},
		{name: "underpaid", amount: 69, want: false},
		{name: "overpaid", amount: 71, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"result":{"meta":{"err":null},"transaction":{"message":{"instructions":[{"program":"system","parsed":{"type":"transfer","info":{"destination":"recipient","lamports":%d}}}]}}}}`, test.amount)
			}))
			defer server.Close()
			validator := NewValidator(server.URL, time.Second)
			got, err := validator.VerifyTransaction("sig", "recipient", 70)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("VerifyTransaction returned %t, want %t", got, test.want)
			}
		})
	}
}

func TestVerifyTransactionCountsEveryTransferToRecipient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"meta":{"err":null},"transaction":{"message":{"instructions":[{"program":"system","parsed":{"type":"transfer","info":{"destination":"recipient","lamports":70}}},{"program":"system","parsed":{"type":"transfer","info":{"destination":"recipient","lamports":1}}}]}}}}`))
	}))
	defer server.Close()
	validator := NewValidator(server.URL, time.Second)
	valid, err := validator.VerifyTransaction("sig", "recipient", 70)
	if err != nil {
		t.Fatal(err)
	}
	if valid {
		t.Fatal("transaction with an extra transfer to the recipient exceeded the quote")
	}
}
