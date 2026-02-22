package tokenexchange

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// exchangeCache caches exchange results keyed by SHA-256 of the subject token.
type exchangeCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	issuedToken string
	expiresAt   time.Time
}

func newExchangeCache(ttl time.Duration) *exchangeCache {
	if ttl <= 0 {
		return nil
	}
	c := &exchangeCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
	go c.cleanup()
	return c
}

func (c *exchangeCache) cacheKey(subjectToken string) string {
	h := sha256.Sum256([]byte(subjectToken))
	return hex.EncodeToString(h[:])
}

func (c *exchangeCache) get(subjectToken string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[c.cacheKey(subjectToken)]
	if !ok || time.Now().After(entry.expiresAt) {
		return "", false
	}
	return entry.issuedToken, true
}

func (c *exchangeCache) put(subjectToken, issuedToken string) {
	c.mu.Lock()
	c.entries[c.cacheKey(subjectToken)] = &cacheEntry{
		issuedToken: issuedToken,
		expiresAt:   time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *exchangeCache) cleanup() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, entry := range c.entries {
			if now.After(entry.expiresAt) {
				delete(c.entries, k)
			}
		}
		c.mu.Unlock()
	}
}

func (c *exchangeCache) size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}
