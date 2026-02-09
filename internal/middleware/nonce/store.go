package nonce

import (
	"context"
	"sync"
	"time"
)

// NonceStore provides atomic check-and-store for nonce values.
type NonceStore interface {
	// CheckAndStore atomically checks if nonce exists; if not, stores it with TTL.
	// Returns true if new (allowed), false if duplicate (replay).
	CheckAndStore(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Size() int
	Close()
}

// MemoryStore is an in-memory NonceStore backed by a map with expiry timestamps.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]time.Time // value = expiry time
	cancel  context.CancelFunc
}

// NewMemoryStore creates a new in-memory nonce store.
// It starts a background cleanup goroutine that runs at min(ttl/2, 30s) intervals.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	ctx, cancel := context.WithCancel(context.Background())
	ms := &MemoryStore{
		entries: make(map[string]time.Time),
		cancel:  cancel,
	}
	cleanupInterval := ttl / 2
	if cleanupInterval > 30*time.Second {
		cleanupInterval = 30 * time.Second
	}
	if cleanupInterval < time.Second {
		cleanupInterval = time.Second
	}
	go ms.cleanup(ctx, cleanupInterval)
	return ms
}

// CheckAndStore atomically checks if the nonce exists; if not, stores it.
func (ms *MemoryStore) CheckAndStore(_ context.Context, key string, ttl time.Duration) (bool, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	now := time.Now()

	// Check if key exists and not expired
	if expiry, exists := ms.entries[key]; exists {
		if now.Before(expiry) {
			return false, nil // duplicate
		}
		// Expired entry — treat as new
	}

	ms.entries[key] = now.Add(ttl)
	return true, nil
}

// Size returns the number of entries (including expired ones not yet cleaned up).
func (ms *MemoryStore) Size() int {
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return len(ms.entries)
}

// Close stops the cleanup goroutine.
func (ms *MemoryStore) Close() {
	ms.cancel()
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
