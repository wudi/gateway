package auth

import (
	"context"
	"sync"
	"time"
)

// ManagedKey represents a managed API key with metadata.
type ManagedKey struct {
	KeyHash          string        `json:"key_hash"`
	KeyPrefix        string        `json:"key_prefix"`
	ClientID         string        `json:"client_id"`
	Name             string        `json:"name"`
	Roles            []string      `json:"roles,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	ExpiresAt        time.Time     `json:"expires_at,omitempty"`
	Revoked          bool          `json:"revoked"`
	RevokedAt        time.Time     `json:"revoked_at,omitempty"`
	LastUsedAt       time.Time     `json:"last_used_at,omitempty"`
	UsageCount       int64         `json:"usage_count"`
	RateLimit        *KeyRateLimit `json:"rate_limit,omitempty"`
	RotatedFrom      string        `json:"rotated_from,omitempty"`
	RotationDeadline time.Time     `json:"rotation_deadline,omitempty"`
}

// KeyRateLimit defines per-key rate limiting.
type KeyRateLimit struct {
	Rate   int           `json:"rate"`
	Period time.Duration `json:"period"`
	Burst  int           `json:"burst"`
}

// KeyStore is the interface for managed key storage.
type KeyStore interface {
	Lookup(keyHash string) (*ManagedKey, bool)
	Store(keyHash string, key *ManagedKey) error
	Remove(keyHash string) error
	List() map[string]*ManagedKey
	Size() int
	Close()
}

// MemoryKeyStore is an in-memory key store with background cleanup.
type MemoryKeyStore struct {
	mu      sync.RWMutex
	entries map[string]*ManagedKey
	cancel  context.CancelFunc
}

// NewMemoryKeyStore creates a new in-memory key store with cleanup.
func NewMemoryKeyStore(cleanupInterval time.Duration) *MemoryKeyStore {
	if cleanupInterval <= 0 {
		cleanupInterval = 60 * time.Second
	}
	ctx, cancel := context.WithCancel(context.Background())
	ms := &MemoryKeyStore{
		entries: make(map[string]*ManagedKey),
		cancel:  cancel,
	}
	go ms.cleanup(ctx, cleanupInterval)
	return ms
}

func (ms *MemoryKeyStore) cleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ms.mu.Lock()
			now := time.Now()
			for hash, key := range ms.entries {
				// Remove expired keys
				if !key.ExpiresAt.IsZero() && now.After(key.ExpiresAt) {
					delete(ms.entries, hash)
					continue
				}
				// Remove keys past rotation deadline (old rotated keys)
				if !key.RotationDeadline.IsZero() && now.After(key.RotationDeadline) {
					delete(ms.entries, hash)
				}
			}
			ms.mu.Unlock()
		}
	}
}

// Lookup returns the key data for a given hash.
func (ms *MemoryKeyStore) Lookup(keyHash string) (*ManagedKey, bool) {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	key, ok := ms.entries[keyHash]
	return key, ok
}

// Store saves a key to the store.
func (ms *MemoryKeyStore) Store(keyHash string, key *ManagedKey) error {
	ms.mu.Lock()
	ms.entries[keyHash] = key
	ms.mu.Unlock()
	return nil
}

// Remove deletes a key from the store.
func (ms *MemoryKeyStore) Remove(keyHash string) error {
	ms.mu.Lock()
	delete(ms.entries, keyHash)
	ms.mu.Unlock()
	return nil
}

// List returns all keys.
func (ms *MemoryKeyStore) List() map[string]*ManagedKey {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	result := make(map[string]*ManagedKey, len(ms.entries))
	for k, v := range ms.entries {
		result[k] = v
	}
	return result
}

// Size returns the number of stored keys.
func (ms *MemoryKeyStore) Size() int {
	ms.mu.RLock()
	defer ms.mu.RUnlock()
	return len(ms.entries)
}

// Close stops the cleanup goroutine.
func (ms *MemoryKeyStore) Close() {
	ms.cancel()
}
