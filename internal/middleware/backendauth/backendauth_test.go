package backendauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestTokenProvider_Apply(t *testing.T) {
	var reqCount atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.FormValue("grant_type") != "client_credentials" {
			t.Errorf("expected grant_type=client_credentials, got %s", r.FormValue("grant_type"))
		}
		if r.FormValue("client_id") != "test-client" {
			t.Errorf("expected client_id=test-client, got %s", r.FormValue("client_id"))
		}
		if r.FormValue("scope") != "read write" {
			t.Errorf("expected scope='read write', got %s", r.FormValue("scope"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "test-token-123",
			"expires_in":   3600,
			"token_type":   "Bearer",
		})
	}))
	defer ts.Close()

	p, err := New("test-route", config.BackendAuthConfig{
		Enabled:      true,
		Type:         "oauth2_client_credentials",
		TokenURL:     ts.URL + "/token",
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		Scopes:       []string{"read", "write"},
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First apply should fetch token
	r := httptest.NewRequest("GET", "/api/data", nil)
	p.Apply(r)

	if got := r.Header.Get("Authorization"); got != "Bearer test-token-123" {
		t.Errorf("expected Authorization header 'Bearer test-token-123', got %q", got)
	}

	// Second apply should use cached token (no new request)
	r2 := httptest.NewRequest("GET", "/api/data", nil)
	p.Apply(r2)

	if got := r2.Header.Get("Authorization"); got != "Bearer test-token-123" {
		t.Errorf("second apply: expected cached token, got %q", got)
	}

	if reqCount.Load() != 1 {
		t.Errorf("expected 1 token request (cached), got %d", reqCount.Load())
	}

	// Stats
	stats := p.Stats()
	if stats["refreshes"].(int64) != 1 {
		t.Errorf("expected 1 refresh, got %v", stats["refreshes"])
	}
	if stats["errors"].(int64) != 0 {
		t.Errorf("expected 0 errors, got %v", stats["errors"])
	}
}

func TestTokenProvider_TokenRefreshOnExpiry(t *testing.T) {
	var callCount atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := callCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "token-" + time.Now().Format("150405"),
			"expires_in":   1, // expires in 1s - safety margin of 10s means already expired
			"token_type":   "Bearer",
		})
		_ = count
	}))
	defer ts.Close()

	p, err := New("test-route", config.BackendAuthConfig{
		Enabled:      true,
		Type:         "oauth2_client_credentials",
		TokenURL:     ts.URL + "/token",
		ClientID:     "c",
		ClientSecret: "s",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// First apply
	r := httptest.NewRequest("GET", "/", nil)
	p.Apply(r)

	// Token with expires_in=1 minus 10s safety = already expired
	// Second apply should trigger a new refresh
	r2 := httptest.NewRequest("GET", "/", nil)
	p.Apply(r2)

	if callCount.Load() < 2 {
		t.Errorf("expected at least 2 token requests for expired token, got %d", callCount.Load())
	}
}

func TestTokenProvider_ErrorResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid_client"}`))
	}))
	defer ts.Close()

	p, err := New("test-route", config.BackendAuthConfig{
		TokenURL:     ts.URL + "/token",
		ClientID:     "bad",
		ClientSecret: "bad",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Apply should not set Authorization on error
	r := httptest.NewRequest("GET", "/", nil)
	p.Apply(r)

	if got := r.Header.Get("Authorization"); got != "" {
		t.Errorf("expected no Authorization on error, got %q", got)
	}
	if p.errors.Load() != 1 {
		t.Errorf("expected 1 error, got %d", p.errors.Load())
	}
}

func TestTokenProvider_ExtraParams(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("audience") != "https://api.example.com" {
			t.Errorf("expected extra param audience, got %q", r.FormValue("audience"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"expires_in":   3600,
		})
	}))
	defer ts.Close()

	p, err := New("test-route", config.BackendAuthConfig{
		TokenURL:     ts.URL + "/token",
		ClientID:     "c",
		ClientSecret: "s",
		ExtraParams:  map[string]string{"audience": "https://api.example.com"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	p.Apply(r)
}

func TestTokenProvider_InvalidURL(t *testing.T) {
	_, err := New("test-route", config.BackendAuthConfig{
		TokenURL:     "not-a-url",
		ClientID:     "c",
		ClientSecret: "s",
	})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestBackendAuthByRoute(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "tok",
			"expires_in":   3600,
		})
	}))
	defer ts.Close()

	m := NewBackendAuthByRoute()

	err := m.AddRoute("r1", config.BackendAuthConfig{
		Enabled:      true,
		Type:         "oauth2_client_credentials",
		TokenURL:     ts.URL + "/token",
		ClientID:     "c",
		ClientSecret: "s",
	})
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}

	if p := m.Lookup("r1"); p == nil {
		t.Fatal("expected provider for r1")
	}
	if p := m.Lookup("nonexistent"); p != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "r1" {
		t.Errorf("expected [r1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["r1"]; !ok {
		t.Error("expected stats for r1")
	}
}

func TestTokenProvider_Middleware(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "mw-token",
			"expires_in":   3600,
		})
	}))
	defer ts.Close()

	p, err := New("test-route", config.BackendAuthConfig{
		TokenURL:     ts.URL + "/token",
		ClientID:     "c",
		ClientSecret: "s",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var gotAuth string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(200)
	})

	handler := p.Middleware()(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if gotAuth != "Bearer mw-token" {
		t.Errorf("expected 'Bearer mw-token', got %q", gotAuth)
	}
}
