package tokenrevoke

import (
	"context"
	"sync"
	"time"
)

// TokenStore is the interface for token revocation storage.
type TokenStore interface {
	Contains(ctx context.Context, key string) (bool, error)
	Add(ctx context.Context, key string, ttl time.Duration) error
	Remove(ctx context.Context, key string) error
	Size() int
	Close()
}

// MemoryStore is an in-memory token revocation store.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]time.Time // key -> expiry
	cancel  context.CancelFunc
}

// NewMemoryStore creates a new in-memory token store with background cleanup.
func NewMemoryStore(cleanupInterval time.Duration) *MemoryStore {
	if cleanupInterval <= 0 {
		cleanupInterval = 60 * time.Second
	}
	if cleanupInterval > 60*time.Second {
		cleanupInterval = 60 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	ms := &MemoryStore{
		entries: make(map[string]time.Time),
		cancel:  cancel,
	}
	go ms.cleanup(ctx, cleanupInterval)
	return ms
}

func (ms *MemoryStore) cleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ms.mu.Lock()
			now := time.Now()
			for key, expiry := range ms.entries {
				if now.After(expiry) {
					delete(ms.entries, key)
				}
			}
			ms.mu.Unlock()
		}
	}
}

// Contains checks if a key is in the store and not expired.
func (ms *MemoryStore) Contains(_ context.Context, key string) (bool, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	expiry, ok := ms.entries[key]
	if !ok {
		return false, nil
	}
	if time.Now().After(expiry) {
		delete(ms.entries, key)
		return false, nil
	}
	return true, nil
}

// Add adds a key to the store with the given TTL.
func (ms *MemoryStore) Add(_ context.Context, key string, ttl time.Duration) error {
	ms.mu.Lock()
	ms.entries[key] = time.Now().Add(ttl)
	ms.mu.Unlock()
	return nil
}

// Remove removes a key from the store.
func (ms *MemoryStore) Remove(_ context.Context, key string) error {
	ms.mu.Lock()
	delete(ms.entries, key)
	ms.mu.Unlock()
	return nil
}

// Size returns the number of entries (including potentially expired ones).
func (ms *MemoryStore) Size() int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return len(ms.entries)
}

// Close stops the cleanup goroutine.
func (ms *MemoryStore) Close() {
	ms.cancel()
}
