package graphql

import (
	"crypto/sha256"
	"fmt"
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

// APQCache implements an Automatic Persisted Queries cache using an LRU.
type APQCache struct {
	cache    *lru.Cache[string, string]
	hits     atomic.Int64
	misses   atomic.Int64
	registers atomic.Int64
}

// NewAPQCache creates a new APQ cache with the given max size.
func NewAPQCache(maxSize int) (*APQCache, error) {
	if maxSize <= 0 {
		maxSize = 1000
	}
	c, err := lru.New[string, string](maxSize)
	if err != nil {
		return nil, fmt.Errorf("apq cache: %w", err)
	}
	return &APQCache{cache: c}, nil
}

// Lookup retrieves a query by its SHA-256 hash. Returns the query and true if found.
func (a *APQCache) Lookup(hash string) (string, bool) {
	q, ok := a.cache.Get(hash)
	if ok {
		a.hits.Add(1)
	} else {
		a.misses.Add(1)
	}
	return q, ok
}

// Register stores a query after verifying its SHA-256 hash matches.
// Returns false if the hash does not match (cache poisoning prevention).
func (a *APQCache) Register(hash, query string) bool {
	actual := fmt.Sprintf("%x", sha256.Sum256([]byte(query)))
	if actual != hash {
		return false
	}
	a.cache.Add(hash, query)
	a.registers.Add(1)
	return true
}

// Stats returns APQ cache metrics.
func (a *APQCache) Stats() map[string]interface{} {
	return map[string]interface{}{
		"hits":      a.hits.Load(),
		"misses":    a.misses.Load(),
		"registers": a.registers.Load(),
		"size":      a.cache.Len(),
	}
}
