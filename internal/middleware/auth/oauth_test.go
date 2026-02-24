package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestOAuthIntrospect(t *testing.T) {
	// Mock introspection server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		token := r.Form.Get("token")

		w.Header().Set("Content-Type", "application/json")

		if token == "valid-token" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"active":    true,
				"sub":       "user123",
				"client_id": "client1",
				"scope":     "read write",
			})
		} else {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"active": false,
			})
		}
	}))
	defer server.Close()

	auth, err := NewOAuthAuth(config.OAuthConfig{
		Enabled:          true,
		IntrospectionURL: server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid token", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer valid-token")

		identity, err := auth.Authenticate(r)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if identity.ClientID != "user123" {
			t.Errorf("expected clientID user123, got %s", identity.ClientID)
		}
		if identity.AuthType != "oauth" {
			t.Errorf("expected authType oauth, got %s", identity.AuthType)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Authorization", "Bearer invalid-token")

		_, err := auth.Authenticate(r)
		if err == nil {
			t.Fatal("expected error for invalid token")
		}
	})

	t.Run("no token", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)

		_, err := auth.Authenticate(r)
		if err == nil {
			t.Fatal("expected error for missing token")
		}
	})
}

func TestOAuthCache(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true,
			"sub":    "user1",
		})
	}))
	defer server.Close()

	auth, _ := NewOAuthAuth(config.OAuthConfig{
		Enabled:          true,
		IntrospectionURL: server.URL,
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer cached-token")

	// First call
	auth.Authenticate(r)
	// Second call (should use cache)
	auth.Authenticate(r)

	if callCount != 1 {
		t.Errorf("expected 1 introspection call (cached), got %d", callCount)
	}
}

func TestOAuthScopes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active": true,
			"sub":    "user1",
			"scope":  "read",
		})
	}))
	defer server.Close()

	auth, _ := NewOAuthAuth(config.OAuthConfig{
		Enabled:          true,
		IntrospectionURL: server.URL,
		Scopes:           []string{"read", "write"},
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer scope-token")

	_, err := auth.Authenticate(r)
	if err == nil {
		t.Fatal("expected error for insufficient scopes")
	}
}
