package cache

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
)

// MemoryStore is an in-memory LRU cache implementing Store.
type MemoryStore struct {
	lru       *expirable.LRU[string, *Entry]
	mu        sync.Mutex // only needed for DeleteByPrefix atomicity
	evictions atomic.Int64
	maxSize   int
}

// NewMemoryStore creates a new in-memory LRU store with the given max size and TTL.
func NewMemoryStore(maxSize int, ttl time.Duration) *MemoryStore {
	if maxSize <= 0 {
		maxSize = 1000
	}
	s := &MemoryStore{
		maxSize: maxSize,
	}
	s.lru = expirable.NewLRU[string, *Entry](maxSize, func(key string, value *Entry) {
		s.evictions.Add(1)
	}, ttl)
	return s
}

func (s *MemoryStore) Get(key string) (*Entry, bool) {
	return s.lru.Get(key)
}

func (s *MemoryStore) Set(key string, entry *Entry) {
	s.lru.Add(key, entry)
}

func (s *MemoryStore) Delete(key string) {
	s.lru.Remove(key)
}

func (s *MemoryStore) DeleteByPrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, key := range s.lru.Keys() {
		if strings.HasPrefix(key, prefix) {
			s.lru.Remove(key)
		}
	}
}

func (s *MemoryStore) Purge() {
	s.lru.Purge()
}

func (s *MemoryStore) Stats() StoreStats {
	return StoreStats{
		Size:      s.lru.Len(),
		MaxSize:   s.maxSize,
		Evictions: s.evictions.Load(),
	}
}
