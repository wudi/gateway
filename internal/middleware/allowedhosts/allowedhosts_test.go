package allowedhosts

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestAllowedHosts_ExactMatch(t *testing.T) {
	ah := New(config.AllowedHostsConfig{
		Enabled: true,
		Hosts:   []string{"example.com", "api.example.com"},
	})

	tests := []struct {
		host    string
		allowed bool
	}{
		{"example.com", true},
		{"api.example.com", true},
		{"Example.COM", true}, // case insensitive
		{"evil.com", false},
		{"example.com:8080", true}, // port stripped
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tt.host
		if got := ah.Check(req); got != tt.allowed {
			t.Errorf("host %q: expected allowed=%v, got %v", tt.host, tt.allowed, got)
		}
	}
}

func TestAllowedHosts_WildcardMatch(t *testing.T) {
	ah := New(config.AllowedHostsConfig{
		Enabled: true,
		Hosts:   []string{"*.example.com"},
	})

	tests := []struct {
		host    string
		allowed bool
	}{
		{"api.example.com", true},
		{"sub.api.example.com", true},
		{"example.com", false}, // exact doesn't match wildcard
		{"evil.com", false},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tt.host
		if got := ah.Check(req); got != tt.allowed {
			t.Errorf("host %q: expected allowed=%v, got %v", tt.host, tt.allowed, got)
		}
	}
}

func TestAllowedHosts_MixedMatch(t *testing.T) {
	ah := New(config.AllowedHostsConfig{
		Enabled: true,
		Hosts:   []string{"example.com", "*.example.com"},
	})

	tests := []struct {
		host    string
		allowed bool
	}{
		{"example.com", true},
		{"api.example.com", true},
		{"evil.com", false},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.Host = tt.host
		if got := ah.Check(req); got != tt.allowed {
			t.Errorf("host %q: expected allowed=%v, got %v", tt.host, tt.allowed, got)
		}
	}
}

func TestAllowedHosts_Middleware_Rejected(t *testing.T) {
	ah := New(config.AllowedHostsConfig{
		Enabled: true,
		Hosts:   []string{"example.com"},
	})
	handler := ah.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "evil.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusMisdirectedRequest {
		t.Errorf("expected 421, got %d", rec.Code)
	}
	if ah.rejected.Load() != 1 {
		t.Errorf("expected 1 rejection, got %d", ah.rejected.Load())
	}
}

func TestAllowedHosts_Middleware_Allowed(t *testing.T) {
	ah := New(config.AllowedHostsConfig{
		Enabled: true,
		Hosts:   []string{"example.com"},
	})
	called := false
	handler := ah.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should be called for allowed hosts")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestAllowedHosts_Stats(t *testing.T) {
	ah := New(config.AllowedHostsConfig{
		Enabled: true,
		Hosts:   []string{"example.com", "*.test.com"},
	})
	stats := ah.Stats()
	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	hosts := stats["hosts"].([]string)
	if len(hosts) != 2 {
		t.Errorf("expected 2 hosts, got %d", len(hosts))
	}
}
