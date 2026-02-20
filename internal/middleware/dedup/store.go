package dedup

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// StoredResponse holds a cached response for dedup replay.
type StoredResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// Store is the interface for dedup response storage.
type Store interface {
	// Get retrieves a stored response by fingerprint. Returns nil if not found.
	Get(ctx context.Context, key string) (*StoredResponse, error)
	// Set stores a response with the given fingerprint and TTL.
	Set(ctx context.Context, key string, resp *StoredResponse, ttl time.Duration) error
	// Close releases any resources.
	Close()
}

// MemoryStore is an in-memory dedup store backed by a map with expiry timestamps.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]*memEntry
	cancel  context.CancelFunc
}

type memEntry struct {
	resp   *StoredResponse
	expiry time.Time
}

// NewMemoryStore creates a new in-memory dedup store.
// It starts a background cleanup goroutine.
func NewMemoryStore(ttl time.Duration) *MemoryStore {
	ctx, cancel := context.WithCancel(context.Background())
	ms := &MemoryStore{
		entries: make(map[string]*memEntry),
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

func (ms *MemoryStore) Get(_ context.Context, key string) (*StoredResponse, error) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	e, ok := ms.entries[key]
	if !ok {
		return nil, nil
	}
	if time.Now().After(e.expiry) {
		delete(ms.entries, key)
		return nil, nil
	}
	return e.resp, nil
}

func (ms *MemoryStore) Set(_ context.Context, key string, resp *StoredResponse, ttl time.Duration) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.entries[key] = &memEntry{
		resp:   resp,
		expiry: time.Now().Add(ttl),
	}
	return nil
}

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
			for key, e := range ms.entries {
				if now.After(e.expiry) {
					delete(ms.entries, key)
				}
			}
			ms.mu.Unlock()
		}
	}
}
