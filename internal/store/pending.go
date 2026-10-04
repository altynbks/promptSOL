package store

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const PendingQuoteTTL = 5 * time.Minute

type PaidResponse struct {
	ID        string
	Body      []byte
	Tokens    uint64
	Amount    uint64
	ExpiresAt time.Time
}

// PendingPayments keeps generated answers private until their quoted payment
// is confirmed. Both pending and retryable completed answers have a short TTL.
type PendingPayments struct {
	mu       sync.Mutex
	capacity int
	pending  map[string]PaidResponse
	complete map[string]PaidResponse // transaction signature -> paid response
}

func NewPendingPayments(capacity int) *PendingPayments {
	if capacity < 1 {
		capacity = 1
	}
	return &PendingPayments{
		capacity: capacity,
		pending:  make(map[string]PaidResponse),
		complete: make(map[string]PaidResponse),
	}
}

func (s *PendingPayments) Put(body []byte, tokens, amount uint64) (PaidResponse, bool) {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return PaidResponse{}, false
	}
	now := time.Now()
	quote := PaidResponse{ID: hex.EncodeToString(idBytes[:]), Body: append([]byte(nil), body...), Tokens: tokens, Amount: amount, ExpiresAt: now.Add(PendingQuoteTTL)}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(now)
	if len(s.pending)+len(s.complete) >= s.capacity {
		return PaidResponse{}, false
	}
	s.pending[quote.ID] = quote
	return clonePaidResponse(quote), true
}

func (s *PendingPayments) Get(id string) (PaidResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	quote, ok := s.pending[id]
	return clonePaidResponse(quote), ok
}

func (s *PendingPayments) GetCompleted(id, signature string) (PaidResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(time.Now())
	quote, ok := s.complete[signature]
	if !ok || quote.ID != id {
		return PaidResponse{}, false
	}
	return clonePaidResponse(quote), true
}

// Complete atomically consumes one quote and binds its answer to one payment
// signature. A retry with the same ID/signature can retrieve the same answer.
func (s *PendingPayments) Complete(id, signature string) (PaidResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.prune(now)
	if quote, ok := s.complete[signature]; ok {
		if quote.ID == id {
			return clonePaidResponse(quote), true
		}
		return PaidResponse{}, false
	}
	quote, ok := s.pending[id]
	if !ok {
		return PaidResponse{}, false
	}
	delete(s.pending, id)
	quote.ExpiresAt = now.Add(PendingQuoteTTL)
	s.complete[signature] = quote
	return clonePaidResponse(quote), true
}

func (s *PendingPayments) prune(now time.Time) {
	for id, quote := range s.pending {
		if !now.Before(quote.ExpiresAt) {
			delete(s.pending, id)
		}
	}
	for signature, quote := range s.complete {
		if !now.Before(quote.ExpiresAt) {
			delete(s.complete, signature)
		}
	}
}

func clonePaidResponse(quote PaidResponse) PaidResponse {
	quote.Body = append([]byte(nil), quote.Body...)
	return quote
}
