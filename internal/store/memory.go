package store

import (
	"container/list"
	"sync"
)

// MemoryCache atomically claims a signature. It fails closed at capacity rather
// than evicting a used signature and making it replayable.
type MemoryCache struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	lru      *list.List
}

type entry struct{ signature string }

func NewMemoryCache(capacity int) *MemoryCache {
	if capacity < 1 {
		capacity = 1
	}
	return &MemoryCache{capacity: capacity, items: make(map[string]*list.Element), lru: list.New()}
}

// Claim returns true only for the first caller to claim this signature.
func (c *MemoryCache) Claim(signature string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[signature]; ok {
		c.lru.MoveToFront(el)
		return false
	}
	if c.lru.Len() >= c.capacity {
		return false
	}
	el := c.lru.PushFront(entry{signature})
	c.items[signature] = el
	return true
}
