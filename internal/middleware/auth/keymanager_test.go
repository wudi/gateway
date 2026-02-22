package auth

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateKey(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		KeyLength: 32,
		KeyPrefix: "gw_",
		Store:     store,
	})

	rawKey, err := mgr.GenerateKey("client-1", "test key", []string{"admin"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Key should have prefix
	if !strings.HasPrefix(rawKey, "gw_") {
		t.Errorf("expected gw_ prefix, got %q", rawKey)
	}

	// Key should be prefix + 64 hex chars (32 bytes)
	if len(rawKey) != 3+64 {
		t.Errorf("expected key length %d, got %d", 3+64, len(rawKey))
	}

	// Store should have 1 key
	if store.Size() != 1 {
		t.Errorf("expected 1 key in store, got %d", store.Size())
	}

	// Raw key should NOT be stored directly (hash is stored)
	keys := store.List()
	for _, mk := range keys {
		if mk.KeyHash == rawKey {
			t.Error("raw key should not be stored directly")
		}
		if mk.ClientID != "client-1" {
			t.Errorf("expected client-1, got %q", mk.ClientID)
		}
		if mk.Name != "test key" {
			t.Errorf("expected 'test key', got %q", mk.Name)
		}
	}
}

func TestGenerateKeyWithTTL(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		KeyLength: 32,
		Store:     store,
	})

	rawKey, err := mgr.GenerateKey("client-1", "ttl key", nil, nil, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Authenticate should succeed
	identity, err := mgr.Authenticate(rawKey)
	if err != nil {
		t.Fatalf("expected successful auth, got: %v", err)
	}
	if identity.ClientID != "client-1" {
		t.Errorf("expected client-1, got %q", identity.ClientID)
	}
}

func TestAuthenticateValid(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		KeyLength: 32,
		Store:     store,
	})

	rawKey, _ := mgr.GenerateKey("client-1", "test", []string{"admin", "read"}, nil, 0)

	identity, err := mgr.Authenticate(rawKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identity.ClientID != "client-1" {
		t.Errorf("expected client-1, got %q", identity.ClientID)
	}
	if identity.AuthType != "api_key" {
		t.Errorf("expected api_key, got %q", identity.AuthType)
	}
	if roles, ok := identity.Claims["roles"].([]string); !ok || len(roles) != 2 {
		t.Error("expected roles in claims")
	}
}

func TestAuthenticateInvalidKey(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{Store: store})

	_, err := mgr.Authenticate("invalid-key")
	if err == nil {
		t.Error("expected error for invalid key")
	}
	if !strings.Contains(err.Error(), "Unauthorized") {
		t.Errorf("expected Unauthorized error, got: %v", err)
	}
}

func TestRevocationReturns403(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		KeyPrefix: "gw_",
		Store:     store,
	})

	rawKey, _ := mgr.GenerateKey("client-1", "test", nil, nil, 0)

	// Find the prefix
	keys := mgr.ListKeys()
	var prefix string
	for _, mk := range keys {
		prefix = mk.KeyPrefix
	}

	// Revoke
	if err := mgr.RevokeKey(prefix); err != nil {
		t.Fatal(err)
	}

	// Auth should return 403 (Forbidden)
	_, err := mgr.Authenticate(rawKey)
	if err == nil {
		t.Error("expected error for revoked key")
	}
	if !strings.Contains(err.Error(), "Forbidden") {
		t.Errorf("expected Forbidden error, got: %v", err)
	}

	// Unrevoke
	if err := mgr.UnrevokeKey(prefix); err != nil {
		t.Fatal(err)
	}

	// Auth should succeed again
	identity, err := mgr.Authenticate(rawKey)
	if err != nil {
		t.Fatalf("expected successful auth after unrevoke, got: %v", err)
	}
	if identity.ClientID != "client-1" {
		t.Error("expected client-1")
	}
}

func TestRotation(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		KeyPrefix: "gw_",
		Store:     store,
	})

	oldKey, _ := mgr.GenerateKey("client-1", "test", []string{"admin"}, nil, 0)

	keys := mgr.ListKeys()
	var prefix string
	for _, mk := range keys {
		prefix = mk.KeyPrefix
	}

	// Rotate with grace period
	newKey, err := mgr.RotateKey(prefix, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if newKey == oldKey {
		t.Error("new key should differ from old key")
	}

	// Both keys should work during grace period
	_, err = mgr.Authenticate(oldKey)
	if err != nil {
		t.Errorf("old key should work during grace period: %v", err)
	}
	_, err = mgr.Authenticate(newKey)
	if err != nil {
		t.Errorf("new key should work: %v", err)
	}

	// Should have 2 keys in store
	if store.Size() != 2 {
		t.Errorf("expected 2 keys, got %d", store.Size())
	}
}

func TestPerKeyRateLimit(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		Store: store,
	})

	rl := &KeyRateLimit{
		Rate:   2,
		Period: time.Second,
		Burst:  2,
	}
	rawKey, _ := mgr.GenerateKey("client-1", "limited", nil, rl, 0)

	// First two should succeed (burst=2)
	for i := 0; i < 2; i++ {
		if _, err := mgr.Authenticate(rawKey); err != nil {
			t.Errorf("request %d should succeed: %v", i, err)
		}
	}

	// Third should be rate limited (429)
	_, err := mgr.Authenticate(rawKey)
	if err == nil {
		t.Error("expected rate limit error")
	}
	if !strings.Contains(err.Error(), "Too Many Requests") {
		t.Errorf("expected Too Many Requests error, got: %v", err)
	}
}

func TestDeleteKey(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		KeyPrefix: "gw_",
		Store:     store,
	})

	rawKey, _ := mgr.GenerateKey("client-1", "test", nil, nil, 0)

	keys := mgr.ListKeys()
	var prefix string
	for _, mk := range keys {
		prefix = mk.KeyPrefix
	}

	// Delete
	if err := mgr.DeleteKey(prefix); err != nil {
		t.Fatal(err)
	}

	// Auth should fail
	_, err := mgr.Authenticate(rawKey)
	if err == nil {
		t.Error("expected error after delete")
	}

	// Store should be empty
	if store.Size() != 0 {
		t.Errorf("expected 0 keys, got %d", store.Size())
	}
}

func TestDefaultRateLimit(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		Store: store,
		DefaultRL: &KeyRateLimit{
			Rate:   1,
			Period: time.Second,
			Burst:  1,
		},
	})

	rawKey, _ := mgr.GenerateKey("client-1", "test", nil, nil, 0)

	// First should succeed
	if _, err := mgr.Authenticate(rawKey); err != nil {
		t.Errorf("first request should succeed: %v", err)
	}

	// Second should be rate limited
	_, err := mgr.Authenticate(rawKey)
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestStats(t *testing.T) {
	store := NewMemoryKeyStore(60 * time.Second)
	defer store.Close()

	mgr := NewAPIKeyManager(KeyManagerConfig{
		KeyPrefix: "gw_",
		Store:     store,
	})

	mgr.GenerateKey("c1", "k1", nil, nil, 0)
	mgr.GenerateKey("c2", "k2", nil, nil, 0)

	stats := mgr.Stats()
	if stats["total_keys"].(int) != 2 {
		t.Errorf("expected 2 total keys, got %v", stats["total_keys"])
	}
	if stats["generated"].(int64) != 2 {
		t.Errorf("expected 2 generated, got %v", stats["generated"])
	}
}

func TestMemoryKeyStoreCleanup(t *testing.T) {
	store := NewMemoryKeyStore(50 * time.Millisecond)
	defer store.Close()

	mk := &ManagedKey{
		KeyHash:   "hash1",
		KeyPrefix: "gw_test1",
		ClientID:  "c1",
		ExpiresAt: time.Now().Add(-1 * time.Second), // already expired
	}
	store.Store("hash1", mk)

	// Wait for cleanup
	time.Sleep(150 * time.Millisecond)

	if store.Size() != 0 {
		t.Errorf("expected expired key to be cleaned up, got %d", store.Size())
	}
}
