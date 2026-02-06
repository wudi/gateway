package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

func serveJWKS(t *testing.T, key ecdsa.PublicKey, kid string) *httptest.Server {
	t.Helper()

	jwkKey, err := jwk.FromRaw(&key)
	if err != nil {
		t.Fatalf("jwk.FromRaw: %v", err)
	}
	jwkKey.Set(jwk.KeyIDKey, kid)
	jwkKey.Set(jwk.AlgorithmKey, "ES256")

	set := jwk.NewSet()
	set.AddKey(jwkKey)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(set)
	}))
}

func TestNewJWKSProvider(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	srv := serveJWKS(t, key.PublicKey, "test-key-1")
	defer srv.Close()

	provider, err := NewJWKSProvider(srv.URL, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewJWKSProvider() error: %v", err)
	}
	if provider == nil {
		t.Fatal("NewJWKSProvider() returned nil")
	}
	if provider.url != srv.URL {
		t.Errorf("expected url %q, got %q", srv.URL, provider.url)
	}
}

func TestNewJWKSProvider_InvalidURL(t *testing.T) {
	_, err := NewJWKSProvider("http://localhost:1/nonexistent", time.Minute)
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestNewJWKSProvider_DefaultRefresh(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	srv := serveJWKS(t, key.PublicKey, "test-key-1")
	defer srv.Close()

	provider, err := NewJWKSProvider(srv.URL, 0) // 0 should default to 1 hour
	if err != nil {
		t.Fatalf("NewJWKSProvider() error: %v", err)
	}
	if provider.refresh != time.Hour {
		t.Errorf("expected default refresh 1h, got %v", provider.refresh)
	}
}

func TestJWKSKeyFunc_WithKid(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	srv := serveJWKS(t, key.PublicKey, "my-key")
	defer srv.Close()

	provider, err := NewJWKSProvider(srv.URL, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewJWKSProvider() error: %v", err)
	}

	keyFunc := provider.KeyFunc()

	// Create a token with the matching kid
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "user-1",
	})
	token.Header["kid"] = "my-key"

	// Sign with the private key
	tokenString, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	// Parse with the JWKS key func
	parsed, err := jwt.Parse(tokenString, keyFunc)
	if err != nil {
		t.Fatalf("jwt.Parse() error: %v", err)
	}
	if !parsed.Valid {
		t.Error("expected valid token")
	}

	claims := parsed.Claims.(jwt.MapClaims)
	if sub, _ := claims.GetSubject(); sub != "user-1" {
		t.Errorf("expected sub 'user-1', got %q", sub)
	}
}

func TestJWKSKeyFunc_WithoutKid(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	srv := serveJWKS(t, key.PublicKey, "default-key")
	defer srv.Close()

	provider, err := NewJWKSProvider(srv.URL, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewJWKSProvider() error: %v", err)
	}

	keyFunc := provider.KeyFunc()

	// Token without kid — should fall back to first key
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "user-2",
	})

	tokenString, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	parsed, err := jwt.Parse(tokenString, keyFunc)
	if err != nil {
		t.Fatalf("jwt.Parse() error: %v", err)
	}
	if !parsed.Valid {
		t.Error("expected valid token")
	}
}

func TestJWKSKeyFunc_WrongKid(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	srv := serveJWKS(t, key.PublicKey, "real-key")
	defer srv.Close()

	provider, err := NewJWKSProvider(srv.URL, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewJWKSProvider() error: %v", err)
	}

	keyFunc := provider.KeyFunc()

	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": "user-3",
	})
	token.Header["kid"] = "wrong-key"

	tokenString, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}

	_, err = jwt.Parse(tokenString, keyFunc)
	if err == nil {
		t.Fatal("expected error for wrong kid")
	}
}

func TestJWKSProviderClose(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	srv := serveJWKS(t, key.PublicKey, "test-key")
	defer srv.Close()

	provider, err := NewJWKSProvider(srv.URL, 5*time.Minute)
	if err != nil {
		t.Fatalf("NewJWKSProvider() error: %v", err)
	}

	// Close should not panic
	provider.Close()
}
