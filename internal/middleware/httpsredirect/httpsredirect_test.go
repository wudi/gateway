package httpsredirect

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestHTTPSRedirect_PlainHTTP(t *testing.T) {
	h := New(config.HTTPSRedirectConfig{Enabled: true})
	handler := h.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "http://example.com/path?q=1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://example.com/path?q=1" {
		t.Errorf("unexpected Location: %s", loc)
	}
	if h.redirects.Load() != 1 {
		t.Errorf("expected 1 redirect, got %d", h.redirects.Load())
	}
}

func TestHTTPSRedirect_Permanent(t *testing.T) {
	h := New(config.HTTPSRedirectConfig{Enabled: true, Permanent: true})
	handler := h.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "http://example.com/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("expected 301, got %d", rec.Code)
	}
}

func TestHTTPSRedirect_CustomPort(t *testing.T) {
	h := New(config.HTTPSRedirectConfig{Enabled: true, Port: 8443})
	handler := h.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "http://example.com:8080/path", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if loc != "https://example.com:8443/path" {
		t.Errorf("unexpected Location: %s", loc)
	}
}

func TestHTTPSRedirect_AlreadyHTTPS_TLS(t *testing.T) {
	h := New(config.HTTPSRedirectConfig{Enabled: true})
	called := false
	handler := h.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "https://example.com/path", nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should be called for TLS requests")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestHTTPSRedirect_XForwardedProto(t *testing.T) {
	h := New(config.HTTPSRedirectConfig{Enabled: true})
	called := false
	handler := h.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "http://example.com/path", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should be called when X-Forwarded-Proto is https")
	}
}

func TestHTTPSRedirect_Stats(t *testing.T) {
	h := New(config.HTTPSRedirectConfig{Enabled: true, Port: 8443, Permanent: true})
	stats := h.Stats()
	if stats["port"] != 8443 {
		t.Errorf("expected port 8443, got %v", stats["port"])
	}
	if stats["permanent"] != true {
		t.Errorf("expected permanent true")
	}
}
