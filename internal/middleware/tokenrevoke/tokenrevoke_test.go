package tokenrevoke

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func makeJWT(claims map[string]interface{}) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	return fmt.Sprintf("%s.%s.fakesig", header, payloadB64)
}

func TestTokenChecker_AllowsUnrevokedToken(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	token := makeJWT(map[string]interface{}{"sub": "user-1", "jti": "abc-123"})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if !tc.Check(req) {
		t.Error("expected token to be allowed")
	}
	if tc.checked.Load() != 1 {
		t.Errorf("expected 1 check, got %d", tc.checked.Load())
	}
}

func TestTokenChecker_BlocksRevokedToken(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	token := makeJWT(map[string]interface{}{"sub": "user-1", "jti": "abc-123"})

	// Revoke the token
	if err := tc.Revoke(token, time.Hour); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if tc.Check(req) {
		t.Error("expected token to be blocked (revoked)")
	}
	if tc.revoked.Load() != 1 {
		t.Errorf("expected 1 revoked, got %d", tc.revoked.Load())
	}
}

func TestTokenChecker_RevokeByJTI(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	// Revoke by JTI directly
	if err := tc.Revoke("abc-123", time.Hour); err != nil {
		t.Fatalf("Revoke failed: %v", err)
	}

	// Token with matching JTI should be blocked
	token := makeJWT(map[string]interface{}{"sub": "user-1", "jti": "abc-123"})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if tc.Check(req) {
		t.Error("expected token to be blocked by JTI revocation")
	}
}

func TestTokenChecker_Unrevoke(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	token := makeJWT(map[string]interface{}{"sub": "user-1", "jti": "abc-123"})

	// Revoke then unrevoke
	tc.Revoke(token, time.Hour)
	tc.Unrevoke(token)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	if !tc.Check(req) {
		t.Error("expected token to be allowed after unrevoke")
	}
}

func TestTokenChecker_NoAuthHeader(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	req := httptest.NewRequest("GET", "/", nil)
	// No Authorization header

	if !tc.Check(req) {
		t.Error("expected request without auth header to be allowed")
	}
	if tc.checked.Load() != 0 {
		t.Errorf("expected 0 checks for no-auth request, got %d", tc.checked.Load())
	}
}

func TestTokenChecker_NonBearerAuth(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")

	if !tc.Check(req) {
		t.Error("expected non-Bearer auth to be allowed")
	}
}

func TestTokenKey_WithJTI(t *testing.T) {
	token := makeJWT(map[string]interface{}{"sub": "user-1", "jti": "my-jti-value"})
	key := tokenKey(token)
	if key != "my-jti-value" {
		t.Errorf("expected key 'my-jti-value', got %q", key)
	}
}

func TestTokenKey_WithoutJTI(t *testing.T) {
	token := makeJWT(map[string]interface{}{"sub": "user-1"})
	key := tokenKey(token)
	if len(key) != 32 {
		t.Errorf("expected 32-char hex hash, got %d chars: %q", len(key), key)
	}
}

func TestTokenExpTTL(t *testing.T) {
	// Token expiring in 1 hour
	exp := time.Now().Add(time.Hour).Unix()
	token := makeJWT(map[string]interface{}{"exp": float64(exp)})
	ttl := tokenExpTTL(token)
	if ttl < 59*time.Minute || ttl > 61*time.Minute {
		t.Errorf("expected TTL ~1h, got %v", ttl)
	}

	// Token already expired
	expPast := time.Now().Add(-time.Hour).Unix()
	tokenExpired := makeJWT(map[string]interface{}{"exp": float64(expPast)})
	ttlExpired := tokenExpTTL(tokenExpired)
	if ttlExpired != 0 {
		t.Errorf("expected TTL 0 for expired token, got %v", ttlExpired)
	}
}

func TestMemoryStore_ContainsAndExpiry(t *testing.T) {
	ms := NewMemoryStore(100 * time.Millisecond)
	defer ms.Close()

	ctx := context.Background()

	// Add a key with short TTL
	ms.Add(ctx, "key1", 50*time.Millisecond)

	ok, err := ms.Contains(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("expected key1 to be found")
	}

	// Wait for expiry
	time.Sleep(100 * time.Millisecond)

	ok, err = ms.Contains(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("expected key1 to be expired")
	}
}

func TestMemoryStore_Remove(t *testing.T) {
	ms := NewMemoryStore(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	ms.Add(ctx, "key1", time.Hour)
	ms.Remove(ctx, "key1")

	ok, _ := ms.Contains(ctx, "key1")
	if ok {
		t.Error("expected key1 to be removed")
	}
}

func TestMemoryStore_Size(t *testing.T) {
	ms := NewMemoryStore(time.Minute)
	defer ms.Close()

	ctx := context.Background()
	ms.Add(ctx, "key1", time.Hour)
	ms.Add(ctx, "key2", time.Hour)

	if ms.Size() != 2 {
		t.Errorf("expected size 2, got %d", ms.Size())
	}
}

func TestTokenChecker_Stats(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	stats := tc.Stats()
	if stats["checked"] != int64(0) {
		t.Errorf("expected 0 checked, got %v", stats["checked"])
	}
	if stats["revoked"] != int64(0) {
		t.Errorf("expected 0 revoked, got %v", stats["revoked"])
	}
}

func TestTokenChecker_DefaultTTL(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true, DefaultTTL: 2 * time.Hour}, nil)
	defer tc.Close()

	if tc.defaultTTL != 2*time.Hour {
		t.Errorf("expected 2h default TTL, got %v", tc.defaultTTL)
	}
}

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		auth     string
		expected string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", "abc123"},
		{"BEARER abc123", "abc123"},
		{"Basic dXNlcjpwYXNz", ""},
		{"", ""},
		{"Bearer ", ""},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		if tt.auth != "" {
			req.Header.Set("Authorization", tt.auth)
		}
		got := extractBearerToken(req)
		if got != tt.expected {
			t.Errorf("extractBearerToken(%q) = %q, want %q", tt.auth, got, tt.expected)
		}
	}
}

// Test that revoking with TTL > defaultTTL caps at defaultTTL
func TestTokenChecker_RevokeTTLCap(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true, DefaultTTL: time.Hour}, nil)
	defer tc.Close()

	// Revoke with large TTL — should be capped
	err := tc.Revoke("some-jti", 100*time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	// Verify it was stored
	ok, _ := tc.store.Contains(context.Background(), "some-jti")
	if !ok {
		t.Error("expected JTI to be stored")
	}
}

func TestRevokeUnrevokeByJTI(t *testing.T) {
	tc := New(config.TokenRevocationConfig{Enabled: true}, nil)
	defer tc.Close()

	tc.Revoke("my-jti", time.Hour)
	ok, _ := tc.store.Contains(context.Background(), "my-jti")
	if !ok {
		t.Error("expected JTI in store")
	}

	tc.Unrevoke("my-jti")
	ok, _ = tc.store.Contains(context.Background(), "my-jti")
	if ok {
		t.Error("expected JTI to be removed")
	}
}
