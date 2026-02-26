package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/variables"
	"golang.org/x/time/rate"
)

// APIKeyManager provides key generation, rotation, revocation, and per-key rate limits.
type APIKeyManager struct {
	store        KeyStore
	keyLength    int    // bytes (default 32 → 64 hex chars)
	keyPrefix    string // e.g., "gw_"
	defaultRL    *KeyRateLimit
	rateLimiters map[string]*rate.Limiter // keyHash → per-key limiter
	mu           sync.RWMutex
	metrics      KeyManagerMetrics
}

// KeyManagerMetrics tracks manager-level activity.
type KeyManagerMetrics struct {
	Generated   atomic.Int64
	Rotated     atomic.Int64
	Revoked     atomic.Int64
	RateLimited atomic.Int64
}

// KeyManagerConfig holds parameters for creating an APIKeyManager.
type KeyManagerConfig struct {
	KeyLength    int
	KeyPrefix    string
	DefaultRL    *KeyRateLimit
	Store        KeyStore
}

// NewAPIKeyManager creates a new key manager.
func NewAPIKeyManager(cfg KeyManagerConfig) *APIKeyManager {
	keyLen := cfg.KeyLength
	if keyLen <= 0 {
		keyLen = 32
	}
	return &APIKeyManager{
		store:        cfg.Store,
		keyLength:    keyLen,
		keyPrefix:    cfg.KeyPrefix,
		defaultRL:    cfg.DefaultRL,
		rateLimiters: make(map[string]*rate.Limiter),
	}
}

// GenerateKey creates a new managed key and returns the raw key (only time visible).
func (m *APIKeyManager) GenerateKey(clientID, name string, roles []string, rl *KeyRateLimit, ttl time.Duration) (string, error) {
	rawBytes := make([]byte, m.keyLength)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", fmt.Errorf("generating random key: %w", err)
	}
	rawKey := m.keyPrefix + hex.EncodeToString(rawBytes)

	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	now := time.Now()
	mk := &ManagedKey{
		KeyHash:   keyHash,
		KeyPrefix: rawKey[:min(len(rawKey), 8)],
		ClientID:  clientID,
		Name:      name,
		Roles:     roles,
		CreatedAt: now,
		RateLimit: rl,
	}
	if ttl > 0 {
		mk.ExpiresAt = now.Add(ttl)
	}

	if err := m.store.Store(keyHash, mk); err != nil {
		return "", err
	}

	// Set up rate limiter
	effectiveRL := rl
	if effectiveRL == nil {
		effectiveRL = m.defaultRL
	}
	if effectiveRL != nil {
		m.mu.Lock()
		m.rateLimiters[keyHash] = rate.NewLimiter(
			rate.Every(effectiveRL.Period/time.Duration(effectiveRL.Rate)),
			effectiveRL.Burst,
		)
		m.mu.Unlock()
	}

	m.metrics.Generated.Add(1)
	return rawKey, nil
}

// RotateKey creates a new key replacing the one identified by prefix, with a grace period for the old key.
func (m *APIKeyManager) RotateKey(keyPrefix string, gracePeriod time.Duration) (string, error) {
	oldKey, oldHash := m.findByPrefix(keyPrefix)
	if oldKey == nil {
		return "", errors.ErrNotFound.WithDetails("key not found")
	}
	if oldKey.Revoked {
		return "", errors.ErrForbidden.WithDetails("cannot rotate a revoked key")
	}

	// Generate new key with same metadata
	newRawKey, err := m.GenerateKey(oldKey.ClientID, oldKey.Name, oldKey.Roles, oldKey.RateLimit, 0)
	if err != nil {
		return "", err
	}

	// Set expiration on new key to match old key's original expiration
	newHash := sha256.Sum256([]byte(newRawKey))
	newKeyHash := hex.EncodeToString(newHash[:])
	if newMK, ok := m.store.Lookup(newKeyHash); ok {
		newMK.ExpiresAt = oldKey.ExpiresAt
		newMK.RotatedFrom = oldHash
		m.store.Store(newKeyHash, newMK)
	}

	// Mark old key with rotation deadline
	oldKey.RotationDeadline = time.Now().Add(gracePeriod)
	m.store.Store(oldHash, oldKey)

	m.metrics.Rotated.Add(1)
	return newRawKey, nil
}

// RevokeKey marks a key as revoked (returns 403 on use).
func (m *APIKeyManager) RevokeKey(keyPrefix string) error {
	mk, hash := m.findByPrefix(keyPrefix)
	if mk == nil {
		return errors.ErrNotFound.WithDetails("key not found")
	}
	mk.Revoked = true
	mk.RevokedAt = time.Now()
	return m.store.Store(hash, mk)
}

// UnrevokeKey restores a revoked key.
func (m *APIKeyManager) UnrevokeKey(keyPrefix string) error {
	mk, hash := m.findByPrefix(keyPrefix)
	if mk == nil {
		return errors.ErrNotFound.WithDetails("key not found")
	}
	mk.Revoked = false
	mk.RevokedAt = time.Time{}
	return m.store.Store(hash, mk)
}

// DeleteKey permanently removes a key.
func (m *APIKeyManager) DeleteKey(keyPrefix string) error {
	mk, hash := m.findByPrefix(keyPrefix)
	if mk == nil {
		return errors.ErrNotFound.WithDetails("key not found")
	}
	m.mu.Lock()
	delete(m.rateLimiters, hash)
	m.mu.Unlock()
	return m.store.Remove(hash)
}

// Authenticate validates a raw key and returns the identity.
func (m *APIKeyManager) Authenticate(rawKey string) (*variables.Identity, error) {
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(hash[:])

	mk, ok := m.store.Lookup(keyHash)
	if !ok {
		return nil, errors.ErrUnauthorized.WithDetails("Invalid API key")
	}

	// Check revocation (403, not 401)
	if mk.Revoked {
		return nil, errors.ErrForbidden.WithDetails("API key has been revoked")
	}

	// Check expiration
	if !mk.ExpiresAt.IsZero() && time.Now().After(mk.ExpiresAt) {
		return nil, errors.ErrUnauthorized.WithDetails("API key has expired")
	}

	// Check rotation deadline
	if !mk.RotationDeadline.IsZero() && time.Now().After(mk.RotationDeadline) {
		return nil, errors.ErrUnauthorized.WithDetails("API key rotation period has expired")
	}

	// Check per-key rate limit
	m.mu.RLock()
	limiter := m.rateLimiters[keyHash]
	m.mu.RUnlock()
	if limiter != nil && !limiter.Allow() {
		m.metrics.RateLimited.Add(1)
		return nil, errors.ErrTooManyRequests.WithDetails("API key rate limit exceeded")
	}

	// Update usage
	mk.LastUsedAt = time.Now()
	mk.UsageCount++
	m.store.Store(keyHash, mk)

	claims := map[string]interface{}{
		"client_id": mk.ClientID,
	}
	if len(mk.Roles) > 0 {
		claims["roles"] = mk.Roles
	}

	return &variables.Identity{
		ClientID: mk.ClientID,
		AuthType: "api_key",
		Claims:   claims,
	}, nil
}

// Stats returns manager statistics.
func (m *APIKeyManager) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total_keys":   m.store.Size(),
		"generated":    m.metrics.Generated.Load(),
		"rotated":      m.metrics.Rotated.Load(),
		"revoked":      m.metrics.Revoked.Load(),
		"rate_limited": m.metrics.RateLimited.Load(),
	}
}

// ListKeys returns all managed keys.
func (m *APIKeyManager) ListKeys() map[string]*ManagedKey {
	return m.store.List()
}

// Close closes the underlying store.
func (m *APIKeyManager) Close() {
	m.store.Close()
}

// findByPrefix finds a key by its display prefix.
func (m *APIKeyManager) findByPrefix(prefix string) (*ManagedKey, string) {
	keys := m.store.List()
	for hash, mk := range keys {
		if mk.KeyPrefix == prefix {
			return mk, hash
		}
	}
	return nil, ""
}
