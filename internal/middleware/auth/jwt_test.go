package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/wudi/gateway/config"
)

func TestJWTAuth(t *testing.T) {
	secret := "test-secret-key"
	cfg := config.JWTConfig{
		Enabled:   true,
		Secret:    secret,
		Issuer:    "test-issuer",
		Algorithm: "HS256",
	}

	auth, err := NewJWTAuth(cfg)
	if err != nil {
		t.Fatalf("failed to create JWT auth: %v", err)
	}

	// Generate a valid token
	token, err := auth.GenerateToken(map[string]interface{}{
		"sub": "user-123",
		"iss": "test-issuer",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	// Test authentication with valid token
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	identity, err := auth.Authenticate(req)
	if err != nil {
		t.Errorf("expected successful auth, got error: %v", err)
	}

	if identity == nil {
		t.Fatal("expected identity, got nil")
	}

	if identity.ClientID != "user-123" {
		t.Errorf("expected client_id 'user-123', got '%s'", identity.ClientID)
	}

	if identity.AuthType != "jwt" {
		t.Errorf("expected auth_type 'jwt', got '%s'", identity.AuthType)
	}
}

func TestJWTAuthInvalidToken(t *testing.T) {
	cfg := config.JWTConfig{
		Enabled:   true,
		Secret:    "test-secret",
		Algorithm: "HS256",
	}

	auth, _ := NewJWTAuth(cfg)

	tests := []struct {
		name       string
		authHeader string
	}{
		{
			name:       "no header",
			authHeader: "",
		},
		{
			name:       "invalid format",
			authHeader: "InvalidToken",
		},
		{
			name:       "malformed token",
			authHeader: "Bearer invalid.token.here",
		},
		{
			name:       "wrong secret",
			authHeader: "Bearer " + generateTokenWithSecret("wrong-secret"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			_, err := auth.Authenticate(req)
			if err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestJWTAuthExpiredToken(t *testing.T) {
	cfg := config.JWTConfig{
		Enabled:   true,
		Secret:    "test-secret",
		Algorithm: "HS256",
	}

	auth, _ := NewJWTAuth(cfg)

	// Generate expired token
	token, _ := auth.GenerateToken(map[string]interface{}{
		"sub": "user-123",
		"exp": time.Now().Add(-time.Hour).Unix(), // expired
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	_, err := auth.Authenticate(req)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestJWTAuthIssuerValidation(t *testing.T) {
	cfg := config.JWTConfig{
		Enabled:   true,
		Secret:    "test-secret",
		Issuer:    "valid-issuer",
		Algorithm: "HS256",
	}

	auth, _ := NewJWTAuth(cfg)

	// Generate token with wrong issuer
	token, _ := auth.GenerateToken(map[string]interface{}{
		"sub": "user-123",
		"iss": "wrong-issuer",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	_, err := auth.Authenticate(req)
	if err == nil {
		t.Error("expected error for wrong issuer")
	}
}

func TestJWTMiddleware(t *testing.T) {
	cfg := config.JWTConfig{
		Enabled:   true,
		Secret:    "test-secret",
		Algorithm: "HS256",
	}

	auth, _ := NewJWTAuth(cfg)

	handler := auth.Middleware(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test without token
	req := httptest.NewRequest("GET", "/api/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rr.Code)
	}

	// Test with valid token
	token, _ := auth.GenerateToken(map[string]interface{}{
		"sub": "user-123",
		"exp": time.Now().Add(time.Hour).Unix(),
	})

	req = httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func generateTokenWithSecret(secret string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tokenString, _ := token.SignedString([]byte(secret))
	return tokenString
}
