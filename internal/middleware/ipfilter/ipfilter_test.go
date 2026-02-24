package ipfilter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestFilterDenyFirst(t *testing.T) {
	f, err := New(config.IPFilterConfig{
		Enabled: true,
		Deny:    []string{"10.0.0.0/8"},
		Order:   "deny_first",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		ip      string
		allowed bool
	}{
		{"denied IP", "10.0.0.1", false},
		{"allowed IP", "192.168.1.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.ip + ":1234"

			if got := f.Check(r); got != tt.allowed {
				t.Errorf("Check() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestFilterAllowFirst(t *testing.T) {
	f, err := New(config.IPFilterConfig{
		Enabled: true,
		Allow:   []string{"192.168.0.0/16"},
		Order:   "allow_first",
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		ip      string
		allowed bool
	}{
		{"in allow list", "192.168.1.1", true},
		{"not in allow list", "10.0.0.1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.ip + ":1234"

			if got := f.Check(r); got != tt.allowed {
				t.Errorf("Check() = %v, want %v", got, tt.allowed)
			}
		})
	}
}

func TestFilterDisabled(t *testing.T) {
	f, _ := New(config.IPFilterConfig{
		Enabled: false,
		Deny:    []string{"0.0.0.0/0"},
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:1234"

	if !f.Check(r) {
		t.Error("disabled filter should allow all")
	}
}

func TestFilterByRoute(t *testing.T) {
	m := NewIPFilterByRoute()

	err := m.AddRoute("route1", config.IPFilterConfig{
		Enabled: true,
		Deny:    []string{"10.0.0.0/8"},
		Order:   "deny_first",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"

	if m.CheckRequest("route1", r) {
		t.Error("should deny 10.x IP")
	}

	if !m.CheckRequest("unknown-route", r) {
		t.Error("unknown route should allow all")
	}
}

func TestRejectRequest(t *testing.T) {
	w := httptest.NewRecorder()
	RejectRequest(w)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}
