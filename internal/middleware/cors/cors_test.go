package cors

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func mustNew(t *testing.T, cfg config.CORSConfig) *Handler {
	t.Helper()
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestCORSPreflight(t *testing.T) {
	h := mustNew(t, config.CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"https://example.com"},
		AllowMethods: []string{"GET", "POST"},
		AllowHeaders: []string{"Content-Type", "Authorization"},
		MaxAge:       3600,
	})

	r := httptest.NewRequest("OPTIONS", "/", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "POST")

	if !h.IsPreflight(r) {
		t.Fatal("should be preflight")
	}

	w := httptest.NewRecorder()
	h.HandlePreflight(w, r)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("expected origin https://example.com, got %s", got)
	}

	if got := w.Header().Get("Access-Control-Allow-Methods"); got != "GET, POST" {
		t.Errorf("expected methods GET, POST, got %s", got)
	}
}

func TestCORSPreflightDisallowedOrigin(t *testing.T) {
	h := mustNew(t, config.CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"https://example.com"},
	})

	r := httptest.NewRequest("OPTIONS", "/", nil)
	r.Header.Set("Origin", "https://evil.com")
	r.Header.Set("Access-Control-Request-Method", "POST")

	w := httptest.NewRecorder()
	h.HandlePreflight(w, r)

	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set allow origin for disallowed origin")
	}
}

func TestCORSWildcardOrigin(t *testing.T) {
	h := mustNew(t, config.CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"*"},
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://any-origin.com")

	w := httptest.NewRecorder()
	h.ApplyHeaders(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("expected *, got %s", got)
	}
}

func TestCORSCredentialsWithExplicitOrigin(t *testing.T) {
	h := mustNew(t, config.CORSConfig{
		Enabled:          true,
		AllowOrigins:     []string{"https://example.com"},
		AllowCredentials: true,
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://example.com")

	w := httptest.NewRecorder()
	h.ApplyHeaders(w, r)

	if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("expected credentials true, got %s", got)
	}

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("with credentials, should echo exact origin, got %s", got)
	}
}

func TestCORSWildcardSubdomain(t *testing.T) {
	h := mustNew(t, config.CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"*.example.com"},
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://app.example.com")

	w := httptest.NewRecorder()
	h.ApplyHeaders(w, r)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("expected echoed origin, got %s", got)
	}
}

func TestCORSRegexPattern(t *testing.T) {
	h := mustNew(t, config.CORSConfig{
		Enabled:             true,
		AllowOriginPatterns: []string{`^https://.*\.example\.com$`},
	})

	// Should match
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://app.example.com")
	w := httptest.NewRecorder()
	h.ApplyHeaders(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Errorf("expected echoed origin for regex match, got %q", got)
	}

	// Should not match
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.Header.Set("Origin", "https://evil.com")
	w2 := httptest.NewRecorder()
	h.ApplyHeaders(w2, r2)
	if got := w2.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no origin for non-matching, got %q", got)
	}
}

func TestCORSPrivateNetwork(t *testing.T) {
	h := mustNew(t, config.CORSConfig{
		Enabled:             true,
		AllowOrigins:        []string{"https://example.com"},
		AllowPrivateNetwork: true,
	})

	r := httptest.NewRequest("OPTIONS", "/", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Private-Network", "true")

	w := httptest.NewRecorder()
	h.HandlePreflight(w, r)

	if got := w.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("expected Access-Control-Allow-Private-Network: true, got %q", got)
	}
}

func TestCORSByRoute(t *testing.T) {
	m := NewCORSByRoute()
	err := m.AddRoute("route1", config.CORSConfig{
		Enabled:      true,
		AllowOrigins: []string{"https://example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}

	h := m.GetHandler("route1")
	if h == nil || !h.IsEnabled() {
		t.Fatal("expected CORS handler for route1")
	}

	if m.GetHandler("unknown") != nil {
		t.Error("expected nil for unknown route")
	}
}
