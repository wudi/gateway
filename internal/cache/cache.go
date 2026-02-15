package cache

import (
	"sync/atomic"
)

// Cache wraps a Store with hit/miss tracking.
type Cache struct {
	store        Store
	hits         atomic.Int64
	misses       atomic.Int64
	notModifieds atomic.Int64
}

// New creates a new Cache backed by the given store.
func New(store Store) *Cache {
	return &Cache{store: store}
}

// Get retrieves an entry from the cache.
func (c *Cache) Get(key string) (*Entry, bool) {
	entry, ok := c.store.Get(key)
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	return entry, true
}

// Set stores an entry in the cache.
func (c *Cache) Set(key string, entry *Entry) {
	c.store.Set(key, entry)
}

// Delete removes a specific key from the cache.
func (c *Cache) Delete(key string) {
	c.store.Delete(key)
}

// DeleteByPrefix removes all keys with the given prefix.
func (c *Cache) DeleteByPrefix(prefix string) {
	c.store.DeleteByPrefix(prefix)
}

// Purge removes all entries from the cache.
func (c *Cache) Purge() {
	c.store.Purge()
}

// RecordNotModified increments the 304 Not Modified counter.
func (c *Cache) RecordNotModified() {
	c.notModifieds.Add(1)
}

// Stats returns cache statistics.
func (c *Cache) Stats() CacheStats {
	ss := c.store.Stats()
	return CacheStats{
		Size:         ss.Size,
		MaxSize:      ss.MaxSize,
		Hits:         c.hits.Load(),
		Misses:       c.misses.Load(),
		Evictions:    ss.Evictions,
		NotModifieds: c.notModifieds.Load(),
	}
}

// CacheStats contains cache statistics.
type CacheStats struct {
	Size         int    `json:"size"`
	MaxSize      int    `json:"max_size"`
	Hits         int64  `json:"hits"`
	Misses       int64  `json:"misses"`
	Evictions    int64  `json:"evictions"`
	NotModifieds int64  `json:"not_modifieds"`
	Bucket       string `json:"bucket,omitempty"`
}
