package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestAPIKeyAuth(t *testing.T) {
	cfg := config.APIKeyConfig{
		Enabled: true,
		Header:  "X-API-Key",
		Keys: []config.APIKeyEntry{
			{Key: "valid-key-1", ClientID: "client-1"},
			{Key: "valid-key-2", ClientID: "client-2"},
		},
	}

	auth := NewAPIKeyAuth(cfg)

	t.Run("ValidAPIKey", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("X-API-Key", "valid-key-1")

		identity, err := auth.Authenticate(req)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if identity == nil {
			t.Fatal("expected identity, got nil")
		}

		if identity.ClientID != "client-1" {
			t.Errorf("expected client_id 'client-1', got '%s'", identity.ClientID)
		}

		if identity.AuthType != "api_key" {
			t.Errorf("expected auth_type 'api_key', got '%s'", identity.AuthType)
		}
	})

	t.Run("InvalidAPIKey", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("X-API-Key", "invalid-key")

		_, err := auth.Authenticate(req)
		if err == nil {
			t.Error("expected error for invalid key")
		}
	})

	t.Run("MissingAPIKey", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/test", nil)

		_, err := auth.Authenticate(req)
		if err == nil {
			t.Error("expected error for missing key")
		}
	})
}

func TestAPIKeyAuthQueryParam(t *testing.T) {
	cfg := config.APIKeyConfig{
		Enabled:    true,
		QueryParam: "api_key",
		Keys: []config.APIKeyEntry{
			{Key: "query-key", ClientID: "query-client"},
		},
	}

	auth := NewAPIKeyAuth(cfg)

	req := httptest.NewRequest("GET", "/api/test?api_key=query-key", nil)

	identity, err := auth.Authenticate(req)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if identity.ClientID != "query-client" {
		t.Errorf("expected client_id 'query-client', got '%s'", identity.ClientID)
	}
}

func TestAPIKeyAuthMiddleware(t *testing.T) {
	cfg := config.APIKeyConfig{
		Enabled: true,
		Header:  "X-API-Key",
		Keys: []config.APIKeyEntry{
			{Key: "test-key", ClientID: "test-client"},
		},
	}

	auth := NewAPIKeyAuth(cfg)

	t.Run("RequiredWithValidKey", func(t *testing.T) {
		handler := auth.Middleware(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/api/test", nil)
		req.Header.Set("X-API-Key", "test-key")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})

	t.Run("RequiredWithoutKey", func(t *testing.T) {
		handler := auth.Middleware(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/api/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rr.Code)
		}
	})

	t.Run("OptionalWithoutKey", func(t *testing.T) {
		handler := auth.Middleware(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("GET", "/api/test", nil)
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rr.Code)
		}
	})
}

func TestAPIKeyAddRemove(t *testing.T) {
	cfg := config.APIKeyConfig{
		Enabled: true,
		Header:  "X-API-Key",
	}

	auth := NewAPIKeyAuth(cfg)

	// Add key
	auth.AddKey("dynamic-key", "dynamic-client")

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-API-Key", "dynamic-key")

	identity, err := auth.Authenticate(req)
	if err != nil {
		t.Errorf("expected no error after adding key, got %v", err)
	}

	if identity.ClientID != "dynamic-client" {
		t.Errorf("expected 'dynamic-client', got '%s'", identity.ClientID)
	}

	// Remove key
	auth.RemoveKey("dynamic-key")

	_, err = auth.Authenticate(req)
	if err == nil {
		t.Error("expected error after removing key")
	}
}

func TestValidateKey(t *testing.T) {
	cfg := config.APIKeyConfig{
		Enabled: true,
		Header:  "X-API-Key",
		Keys: []config.APIKeyEntry{
			{Key: "valid-key", ClientID: "valid-client"},
		},
	}

	auth := NewAPIKeyAuth(cfg)

	clientID, valid := auth.ValidateKey("valid-key")
	if !valid {
		t.Error("expected key to be valid")
	}
	if clientID != "valid-client" {
		t.Errorf("expected 'valid-client', got '%s'", clientID)
	}

	_, valid = auth.ValidateKey("invalid-key")
	if valid {
		t.Error("expected key to be invalid")
	}
}
