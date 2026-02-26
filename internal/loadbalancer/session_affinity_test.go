package loadbalancer

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestSessionAffinityBalancer_CookieHitsHealthyBackend(t *testing.T) {
	backends := []*Backend{
		{URL: "http://backend1:8080", Healthy: true},
		{URL: "http://backend2:8080", Healthy: true},
	}
	inner := NewRoundRobin(backends)

	sa := NewSessionAffinityBalancer(inner, config.SessionAffinityConfig{Enabled: true})

	// Create a request with an affinity cookie pointing to backend2
	req := httptest.NewRequest("GET", "/test", nil)
	encoded := base64.RawURLEncoding.EncodeToString([]byte("http://backend2:8080"))
	req.AddCookie(&http.Cookie{Name: "X-Session-Backend", Value: encoded})

	backend, _ := sa.NextForHTTPRequest(req)
	if backend == nil {
		t.Fatal("expected a backend, got nil")
	}
	if backend.URL != "http://backend2:8080" {
		t.Errorf("expected backend2, got %s", backend.URL)
	}
}

func TestSessionAffinityBalancer_CookieHitsUnhealthyBackend(t *testing.T) {
	backends := []*Backend{
		{URL: "http://backend1:8080", Healthy: true},
		{URL: "http://backend2:8080", Healthy: false},
	}
	inner := NewRoundRobin(backends)

	sa := NewSessionAffinityBalancer(inner, config.SessionAffinityConfig{Enabled: true})

	req := httptest.NewRequest("GET", "/test", nil)
	encoded := base64.RawURLEncoding.EncodeToString([]byte("http://backend2:8080"))
	req.AddCookie(&http.Cookie{Name: "X-Session-Backend", Value: encoded})

	backend, _ := sa.NextForHTTPRequest(req)
	if backend == nil {
		t.Fatal("expected a backend, got nil")
	}
	// Should fall through to inner balancer (round robin picks healthy backend1)
	if backend.URL != "http://backend1:8080" {
		t.Errorf("expected fallback to backend1, got %s", backend.URL)
	}
}

func TestSessionAffinityBalancer_NoCookie(t *testing.T) {
	backends := []*Backend{
		{URL: "http://backend1:8080", Healthy: true},
		{URL: "http://backend2:8080", Healthy: true},
	}
	inner := NewRoundRobin(backends)

	sa := NewSessionAffinityBalancer(inner, config.SessionAffinityConfig{Enabled: true})

	req := httptest.NewRequest("GET", "/test", nil)
	backend, _ := sa.NextForHTTPRequest(req)
	if backend == nil {
		t.Fatal("expected a backend, got nil")
	}
	// Should use inner balancer
	if backend.URL != "http://backend1:8080" && backend.URL != "http://backend2:8080" {
		t.Errorf("unexpected backend: %s", backend.URL)
	}
}

func TestSessionAffinityBalancer_InvalidCookie(t *testing.T) {
	backends := []*Backend{
		{URL: "http://backend1:8080", Healthy: true},
	}
	inner := NewRoundRobin(backends)

	sa := NewSessionAffinityBalancer(inner, config.SessionAffinityConfig{Enabled: true})

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "X-Session-Backend", Value: "not-valid-base64!!!"})

	backend, _ := sa.NextForHTTPRequest(req)
	if backend == nil {
		t.Fatal("expected a backend, got nil")
	}
	if backend.URL != "http://backend1:8080" {
		t.Errorf("expected fallback to backend1, got %s", backend.URL)
	}
}

func TestSessionAffinityBalancer_MakeCookie(t *testing.T) {
	sa := NewSessionAffinityBalancer(NewRoundRobin(nil), config.SessionAffinityConfig{
		Enabled:    true,
		CookieName: "my-affinity",
		TTL:        2 * time.Hour,
		Path:       "/api",
		Secure:     true,
		SameSite:   "strict",
	})

	cookie := sa.MakeCookie("http://backend1:8080")
	if cookie.Name != "my-affinity" {
		t.Errorf("expected cookie name my-affinity, got %s", cookie.Name)
	}
	if cookie.Path != "/api" {
		t.Errorf("expected path /api, got %s", cookie.Path)
	}
	if cookie.MaxAge != 7200 {
		t.Errorf("expected MaxAge 7200, got %d", cookie.MaxAge)
	}
	if !cookie.Secure {
		t.Error("expected Secure=true")
	}
	if !cookie.HttpOnly {
		t.Error("expected HttpOnly=true")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("expected SameSiteStrict, got %v", cookie.SameSite)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != "http://backend1:8080" {
		t.Errorf("expected decoded value http://backend1:8080, got %s", string(decoded))
	}
}

func TestSessionAffinityBalancer_Delegates(t *testing.T) {
	backends := []*Backend{
		{URL: "http://backend1:8080", Healthy: true},
		{URL: "http://backend2:8080", Healthy: true},
	}
	inner := NewRoundRobin(backends)

	sa := NewSessionAffinityBalancer(inner, config.SessionAffinityConfig{Enabled: true})

	// Test delegation methods
	if sa.HealthyCount() != 2 {
		t.Errorf("expected HealthyCount 2, got %d", sa.HealthyCount())
	}

	sa.MarkUnhealthy("http://backend1:8080")
	if sa.HealthyCount() != 1 {
		t.Errorf("expected HealthyCount 1, got %d", sa.HealthyCount())
	}

	sa.MarkHealthy("http://backend1:8080")
	if sa.HealthyCount() != 2 {
		t.Errorf("expected HealthyCount 2, got %d", sa.HealthyCount())
	}

	got := sa.GetBackends()
	if len(got) != 2 {
		t.Errorf("expected 2 backends, got %d", len(got))
	}

	newBackends := []*Backend{
		{URL: "http://backend3:8080", Healthy: true},
	}
	sa.UpdateBackends(newBackends)
	if sa.HealthyCount() != 1 {
		t.Errorf("expected HealthyCount 1 after update, got %d", sa.HealthyCount())
	}
}

func TestSessionAffinityBalancer_DefaultConfig(t *testing.T) {
	sa := NewSessionAffinityBalancer(NewRoundRobin(nil), config.SessionAffinityConfig{Enabled: true})

	if sa.CookieName() != "X-Session-Backend" {
		t.Errorf("expected default cookie name X-Session-Backend, got %s", sa.CookieName())
	}
	if sa.TTL() != time.Hour {
		t.Errorf("expected default TTL 1h, got %v", sa.TTL())
	}
}

func TestSessionAffinityBalancer_Next(t *testing.T) {
	backends := []*Backend{
		{URL: "http://backend1:8080", Healthy: true},
	}
	inner := NewRoundRobin(backends)

	sa := NewSessionAffinityBalancer(inner, config.SessionAffinityConfig{Enabled: true})

	// Next() should delegate to inner
	b := sa.Next()
	if b == nil || b.URL != "http://backend1:8080" {
		t.Errorf("Next() should delegate to inner balancer")
	}
}

// Ensure SessionAffinityBalancer implements both Balancer and RequestAwareBalancer.
var _ Balancer = (*SessionAffinityBalancer)(nil)
var _ RequestAwareBalancer = (*SessionAffinityBalancer)(nil)
