package store

import "testing"

func TestPendingPaymentCanBeConsumedOnlyByOneSignature(t *testing.T) {
	cache := NewPendingPayments(4)
	quote, ok := cache.Put([]byte("paid answer"), 7, 70)
	if !ok {
		t.Fatal("failed to store quote")
	}
	if _, ok := cache.Get(quote.ID); !ok {
		t.Fatal("new quote was not available")
	}
	paid, ok := cache.Complete(quote.ID, "sig-1")
	if !ok || string(paid.Body) != "paid answer" {
		t.Fatal("confirmed quote did not produce its answer")
	}
	if _, ok := cache.Complete(quote.ID, "sig-2"); ok {
		t.Fatal("second payment signature consumed the same quote")
	}
	if _, ok := cache.GetCompleted(quote.ID, "sig-1"); !ok {
		t.Fatal("same signature could not retry answer delivery")
	}
	if _, ok := cache.GetCompleted(quote.ID, "sig-2"); ok {
		t.Fatal("different signature retrieved the paid answer")
	}
}

func TestPendingPaymentsFailClosedAtCapacity(t *testing.T) {
	cache := NewPendingPayments(1)
	if _, ok := cache.Put([]byte("first"), 1, 10); !ok {
		t.Fatal("failed to store first quote")
	}
	if _, ok := cache.Put([]byte("second"), 1, 10); ok {
		t.Fatal("cache exceeded its configured capacity")
	}
}
