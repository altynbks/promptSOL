package proxy

import "testing"

func TestQuoteAmount(t *testing.T) {
	amount, err := QuoteAmount(125, 10)
	if err != nil || amount != 1250 {
		t.Fatalf("QuoteAmount(125, 10) = %d, %v", amount, err)
	}
	if _, err := QuoteAmount(0, 10); err == nil {
		t.Fatal("zero tokens must not produce a payment")
	}
	if _, err := QuoteAmount(^uint64(0), 2); err == nil {
		t.Fatal("overflow must be rejected")
	}
	const maxSafeLamports = uint64(1<<53 - 1)
	if amount, err := QuoteAmount(maxSafeLamports, 1); err != nil || amount != maxSafeLamports {
		t.Fatalf("QuoteAmount at the exact browser limit = %d, %v", amount, err)
	}
	if _, err := QuoteAmount(maxSafeLamports/2+1, 2); err == nil {
		t.Fatal("amounts above the exact browser limit must be rejected")
	}
}
