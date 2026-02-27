package waf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestNew(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled: true,
		Mode:    "block",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if w == nil {
		t.Fatal("New() returned nil")
	}
}

func TestNew_DetectMode(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled: true,
		Mode:    "detect",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if w.mode != "detect" {
		t.Errorf("expected mode 'detect', got %q", w.mode)
	}
}

func TestNew_DefaultMode(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	if w.mode != "block" {
		t.Errorf("expected default mode 'block', got %q", w.mode)
	}
}

func TestMiddleware_PassesCleanRequest(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled:      true,
		Mode:         "block",
		SQLInjection: true,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	called := false
	inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		called = true
		rw.WriteHeader(http.StatusOK)
	})

	handler := w.Middleware()(inner)

	r := httptest.NewRequest("GET", "/api/users?id=123", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, r)

	if !called {
		t.Error("expected next handler to be called for clean request")
	}
	if rw.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rw.Code)
	}
}

func TestMiddleware_BlocksSQLInjection(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled:      true,
		Mode:         "block",
		SQLInjection: true,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	called := false
	inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := w.Middleware()(inner)

	r := httptest.NewRequest("GET", "/api/users?id=1'+OR+'1'='1", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, r)

	if called {
		t.Error("expected next handler NOT to be called for SQL injection")
	}
	if rw.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rw.Code)
	}
}

func TestMiddleware_DetectMode(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled:      true,
		Mode:         "detect",
		SQLInjection: true,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	handler := w.Middleware()(inner)

	// SQL injection payload — in detect mode should NOT block, request passes through
	r := httptest.NewRequest("GET", "/api/users?id=1'+OR+'1'='1", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, r)

	if w.detectedTotal.Load() == 0 {
		t.Error("expected detectedTotal > 0 in detect mode")
	}
}

func TestStats(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled: true,
		Mode:    "block",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	stats := w.Stats()
	if stats["mode"] != "block" {
		t.Errorf("expected mode 'block', got %v", stats["mode"])
	}
	if stats["requests_total"] != int64(0) {
		t.Errorf("expected requests_total 0, got %v", stats["requests_total"])
	}
}

func TestWAFByRoute(t *testing.T) {
	m := NewWAFByRoute()

	err := m.AddRoute("route1", config.WAFConfig{
		Enabled: true,
		Mode:    "block",
	})
	if err != nil {
		t.Fatalf("AddRoute() error: %v", err)
	}

	w := m.Lookup("route1")
	if w == nil {
		t.Fatal("expected WAF for route1")
	}

	if m.Lookup("unknown") != nil {
		t.Error("expected nil for unknown route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("expected [route1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{
			name:       "X-Forwarded-For",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Forwarded-For": "192.168.1.1, 10.0.0.1"},
			want:       "192.168.1.1",
		},
		{
			name:       "X-Real-IP",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{"X-Real-IP": "192.168.1.2"},
			want:       "192.168.1.2",
		},
		{
			name:       "RemoteAddr",
			remoteAddr: "10.0.0.1:1234",
			headers:    map[string]string{},
			want:       "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			got := clientIP(r)
			if got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNew_InlineRules(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled: true,
		Mode:    "block",
		InlineRules: []string{
			`SecRule REQUEST_URI "@contains /admin" "id:9001,phase:1,deny,status:403,msg:'Admin access blocked'"`,
		},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	called := false
	inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		called = true
		rw.WriteHeader(http.StatusOK)
	})

	handler := w.Middleware()(inner)

	// Should be blocked by inline rule
	r := httptest.NewRequest("GET", "/admin/secret", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, r)

	if called {
		t.Error("expected next handler NOT to be called for /admin path")
	}
	if rw.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rw.Code)
	}

	// Non-admin path should pass
	called = false
	r2 := httptest.NewRequest("GET", "/api/users", nil)
	r2.RemoteAddr = "10.0.0.1:1234"
	rw2 := httptest.NewRecorder()
	handler.ServeHTTP(rw2, r2)

	if !called {
		t.Error("expected next handler to be called for /api/users path")
	}
}

func TestMiddleware_RequestsCounter(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled: true,
		Mode:    "block",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.WriteHeader(http.StatusOK)
	})

	handler := w.Middleware()(inner)

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, r)
	}

	if w.requestsTotal.Load() != 5 {
		t.Errorf("expected 5 requests total, got %d", w.requestsTotal.Load())
	}
}

func TestMiddleware_XSSDetection(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled: true,
		Mode:    "block",
		XSS:     true,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	called := false
	inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := w.Middleware()(inner)

	r := httptest.NewRequest("GET", "/search?q=<script>alert('xss')</script>", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, r)

	if called {
		t.Error("expected next handler NOT to be called for XSS payload")
	}
	if rw.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rw.Code)
	}
}

func TestMiddleware_RequestWithBody(t *testing.T) {
	w, err := New(config.WAFConfig{
		Enabled:      true,
		Mode:         "block",
		SQLInjection: true,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	called := false
	inner := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		called = true
		rw.WriteHeader(http.StatusOK)
	})

	handler := w.Middleware()(inner)

	body := `{"name": "John Doe", "email": "john@example.com"}`
	r := httptest.NewRequest("POST", "/api/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "10.0.0.1:1234"
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, r)

	if !called {
		t.Error("expected next handler to be called for clean POST body")
	}
}
