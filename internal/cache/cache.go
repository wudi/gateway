package cache

import (
	"container/list"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Entry represents a cached response
type Entry struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	CreatedAt  time.Time
	TTL        time.Duration
}

// IsExpired returns true if the entry has expired
func (e *Entry) IsExpired() bool {
	return time.Since(e.CreatedAt) > e.TTL
}

// Cache is a thread-safe LRU in-memory cache
type Cache struct {
	maxSize  int
	items    map[string]*list.Element
	order    *list.List
	mu       sync.Mutex
	hits     atomic.Int64
	misses   atomic.Int64
	evictions atomic.Int64
}

type cacheItem struct {
	key   string
	entry *Entry
}

// NewCache creates a new LRU cache with the given max size
func NewCache(maxSize int) *Cache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &Cache{
		maxSize: maxSize,
		items:   make(map[string]*list.Element),
		order:   list.New(),
	}
}

// Get retrieves an entry from the cache
func (c *Cache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}

	item := elem.Value.(*cacheItem)

	// Check expiry
	if item.entry.IsExpired() {
		c.removeElement(elem)
		c.misses.Add(1)
		return nil, false
	}

	// Move to front (most recently used)
	c.order.MoveToFront(elem)
	c.hits.Add(1)
	return item.entry, true
}

// Set stores an entry in the cache
func (c *Cache) Set(key string, entry *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry
	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		elem.Value.(*cacheItem).entry = entry
		return
	}

	// Evict if at capacity
	if c.order.Len() >= c.maxSize {
		c.evictOldest()
	}

	item := &cacheItem{key: key, entry: entry}
	elem := c.order.PushFront(item)
	c.items[key] = elem
}

// Delete removes a specific key from the cache
func (c *Cache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		c.removeElement(elem)
	}
}

// DeleteByPrefix removes all keys with the given prefix
func (c *Cache) DeleteByPrefix(prefix string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var toDelete []*list.Element
	for key, elem := range c.items {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			toDelete = append(toDelete, elem)
		}
	}

	for _, elem := range toDelete {
		c.removeElement(elem)
	}
}

// Purge removes all entries from the cache
func (c *Cache) Purge() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = make(map[string]*list.Element)
	c.order.Init()
}

// Stats returns cache statistics
func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	size := c.order.Len()
	c.mu.Unlock()

	return CacheStats{
		Size:      size,
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

func (c *Cache) evictOldest() {
	elem := c.order.Back()
	if elem != nil {
		c.removeElement(elem)
		c.evictions.Add(1)
	}
}

func (c *Cache) removeElement(elem *list.Element) {
	c.order.Remove(elem)
	item := elem.Value.(*cacheItem)
	delete(c.items, item.key)
}
