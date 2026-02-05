package cache

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
)

// Entry represents a cached response
type Entry struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// Cache is a thread-safe LRU in-memory cache with TTL-based expiration
type Cache struct {
	lru       *expirable.LRU[string, *Entry]
	mu        sync.Mutex // only needed for DeleteByPrefix atomicity
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
	maxSize   int
}

// NewCache creates a new LRU cache with the given max size and TTL
func NewCache(maxSize int, ttl time.Duration) *Cache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	c := &Cache{
		maxSize: maxSize,
	}
	c.lru = expirable.NewLRU[string, *Entry](maxSize, func(key string, value *Entry) {
		c.evictions.Add(1)
	}, ttl)
	return c
}

// Get retrieves an entry from the cache
func (c *Cache) Get(key string) (*Entry, bool) {
	entry, ok := c.lru.Get(key)
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	return entry, true
}

// Set stores an entry in the cache
func (c *Cache) Set(key string, entry *Entry) {
	c.lru.Add(key, entry)
}

// Delete removes a specific key from the cache
func (c *Cache) Delete(key string) {
	c.lru.Remove(key)
}

// DeleteByPrefix removes all keys with the given prefix
func (c *Cache) DeleteByPrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range c.lru.Keys() {
		if strings.HasPrefix(key, prefix) {
			c.lru.Remove(key)
		}
	}
}

// Purge removes all entries from the cache
func (c *Cache) Purge() {
	c.lru.Purge()
}

// Stats returns cache statistics
func (c *Cache) Stats() CacheStats {
	return CacheStats{
		Size:      c.lru.Len(),
		MaxSize:   c.maxSize,
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
	}
}

// CacheStats contains cache statistics
type CacheStats struct {
	Size      int   `json:"size"`
	MaxSize   int   `json:"max_size"`
	Hits      int64 `json:"hits"`
	Misses    int64 `json:"misses"`
	Evictions int64 `json:"evictions"`
}
