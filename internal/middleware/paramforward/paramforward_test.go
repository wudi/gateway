package paramforward

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestParamForwarder_FilterHeaders(t *testing.T) {
	cfg := config.ParamForwardingConfig{
		Enabled: true,
		Headers: []string{"Authorization", "X-Custom"},
	}

	pf := New(cfg)

	var capturedHeaders http.Header
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.WriteHeader(200)
	})

	handler := pf.Middleware()(inner)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("X-Custom", "value")
	req.Header.Set("X-Secret", "should-be-removed")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Authorization and X-Custom should be kept
	if capturedHeaders.Get("Authorization") != "Bearer token" {
		t.Error("Authorization header should be kept")
	}
	if capturedHeaders.Get("X-Custom") != "value" {
		t.Error("X-Custom header should be kept")
	}

	// X-Secret should be removed
	if capturedHeaders.Get("X-Secret") != "" {
		t.Error("X-Secret header should be removed")
	}

	// Essential headers should always be kept
	if capturedHeaders.Get("Content-Type") != "application/json" {
		t.Error("Content-Type (essential) should be kept")
	}
	if capturedHeaders.Get("User-Agent") != "test" {
		t.Error("User-Agent (essential) should be kept")
	}

	if pf.Stripped() == 0 {
		t.Error("expected stripped count > 0")
	}
}

func TestParamForwarder_FilterQuery(t *testing.T) {
	cfg := config.ParamForwardingConfig{
		Enabled:     true,
		QueryParams: []string{"page", "limit"},
	}

	pf := New(cfg)

	var capturedQuery string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
	})

	handler := pf.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test?page=1&limit=10&secret=abc&page=2", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Parse the captured query
	q, _ := http.NewRequest("", "?"+capturedQuery, nil)
	vals := q.URL.Query()

	if vals.Get("page") != "1" {
		t.Errorf("expected page=1, got %s", vals.Get("page"))
	}
	if vals.Get("limit") != "10" {
		t.Errorf("expected limit=10, got %s", vals.Get("limit"))
	}
	if vals.Get("secret") != "" {
		t.Error("secret query param should be removed")
	}
}

func TestParamForwarder_FilterCookies(t *testing.T) {
	cfg := config.ParamForwardingConfig{
		Enabled: true,
		Cookies: []string{"session"},
	}

	pf := New(cfg)

	var capturedCookies []*http.Cookie
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedCookies = r.Cookies()
		w.WriteHeader(200)
	})

	handler := pf.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "abc123"})
	req.AddCookie(&http.Cookie{Name: "tracking", Value: "xyz"})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if len(capturedCookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(capturedCookies))
	}
	if capturedCookies[0].Name != "session" {
		t.Errorf("expected session cookie, got %s", capturedCookies[0].Name)
	}
}

func TestParamForwarder_NoFiltering(t *testing.T) {
	// When no lists are provided, nothing should be filtered
	cfg := config.ParamForwardingConfig{
		Enabled: true,
		Headers: []string{"X-Allowed"},
	}

	pf := New(cfg)

	var capturedQuery string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(200)
	})

	handler := pf.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test?anything=works", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Query params should pass through since no query filter configured
	if capturedQuery != "anything=works" {
		t.Errorf("expected query to pass through, got %s", capturedQuery)
	}
}

func TestParamForwardByRoute(t *testing.T) {
	m := NewParamForwardByRoute()

	cfg := config.ParamForwardingConfig{
		Enabled: true,
		Headers: []string{"Authorization"},
	}

	m.AddRoute("r1", cfg)

	if m.GetForwarder("r1") == nil {
		t.Error("expected forwarder for r1")
	}
	if m.GetForwarder("r2") != nil {
		t.Error("expected nil for r2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 {
		t.Errorf("expected 1 route, got %d", len(ids))
	}

	stats := m.Stats()
	if stats["r1"] == nil {
		t.Error("expected stats for r1")
	}
}
