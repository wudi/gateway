package extauth

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// authCache is a TTL cache for ext auth results.
type authCache struct {
	entries map[string]*cacheEntry
	mu      sync.RWMutex
	ttl     time.Duration
}

type cacheEntry struct {
	result    *ExtAuthResult
	expiresAt time.Time
}

func newAuthCache(ttl time.Duration) *authCache {
	return &authCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// Get returns a cached result if found and not expired.
func (c *authCache) Get(key string) *ExtAuthResult {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok {
		return nil
	}
	if time.Now().After(entry.expiresAt) {
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return nil
	}
	return entry.result
}

// Set stores a result in the cache.
func (c *authCache) Set(key string, result *ExtAuthResult) {
	c.mu.Lock()
	c.entries[key] = &cacheEntry{
		result:    result,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// BuildKey creates a cache key from the request and the configured headers to send.
func BuildKey(r *http.Request, headersToSend map[string]bool) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\n%s\n", r.Method, r.URL.Path)

	if headersToSend == nil {
		// All headers — sort for determinism
		keys := make([]string, 0, len(r.Header))
		for k := range r.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(h, "%s:%s\n", k, strings.Join(r.Header[k], ","))
		}
	} else {
		keys := make([]string, 0, len(headersToSend))
		for k := range headersToSend {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if v := r.Header.Get(k); v != "" {
				fmt.Fprintf(h, "%s:%s\n", k, v)
			}
		}
	}

	return fmt.Sprintf("%x", h.Sum(nil))
}
