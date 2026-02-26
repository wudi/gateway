package claimsprop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/variables"
)

func makeRequest(claims map[string]interface{}) *http.Request {
	req := httptest.NewRequest("GET", "/", nil)
	varCtx := variables.NewContext(req)
	varCtx.Identity = &variables.Identity{
		Claims: claims,
	}
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	return req.WithContext(ctx)
}

func TestClaimsPropagator_BasicClaims(t *testing.T) {
	cp := New(config.ClaimsPropagationConfig{
		Enabled: true,
		Claims: map[string]string{
			"sub":   "X-User-ID",
			"email": "X-User-Email",
		},
	})

	r := makeRequest(map[string]interface{}{
		"sub":   "user-123",
		"email": "user@example.com",
	})

	cp.Apply(r)

	if got := r.Header.Get("X-User-ID"); got != "user-123" {
		t.Errorf("X-User-ID = %q, want %q", got, "user-123")
	}
	if got := r.Header.Get("X-User-Email"); got != "user@example.com" {
		t.Errorf("X-User-Email = %q, want %q", got, "user@example.com")
	}
	if cp.propagated.Load() != 1 {
		t.Errorf("propagated = %d, want 1", cp.propagated.Load())
	}
}

func TestClaimsPropagator_NestedClaims(t *testing.T) {
	cp := New(config.ClaimsPropagationConfig{
		Enabled: true,
		Claims: map[string]string{
			"user.email": "X-User-Email",
			"org.id":     "X-Org-ID",
		},
	})

	r := makeRequest(map[string]interface{}{
		"user": map[string]interface{}{
			"email": "nested@example.com",
		},
		"org": map[string]interface{}{
			"id": float64(42),
		},
	})

	cp.Apply(r)

	if got := r.Header.Get("X-User-Email"); got != "nested@example.com" {
		t.Errorf("X-User-Email = %q, want %q", got, "nested@example.com")
	}
	if got := r.Header.Get("X-Org-ID"); got != "42" {
		t.Errorf("X-Org-ID = %q, want %q", got, "42")
	}
}

func TestClaimsPropagator_MissingClaims(t *testing.T) {
	cp := New(config.ClaimsPropagationConfig{
		Enabled: true,
		Claims: map[string]string{
			"sub":     "X-User-ID",
			"missing": "X-Missing",
		},
	})

	r := makeRequest(map[string]interface{}{
		"sub": "user-123",
	})

	cp.Apply(r)

	if got := r.Header.Get("X-User-ID"); got != "user-123" {
		t.Errorf("X-User-ID = %q, want %q", got, "user-123")
	}
	if got := r.Header.Get("X-Missing"); got != "" {
		t.Errorf("X-Missing should be empty, got %q", got)
	}
}

func TestClaimsPropagator_NoIdentity(t *testing.T) {
	cp := New(config.ClaimsPropagationConfig{
		Enabled: true,
		Claims: map[string]string{
			"sub": "X-User-ID",
		},
	})

	// Request without identity
	req := httptest.NewRequest("GET", "/", nil)
	varCtx := variables.NewContext(req)
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, varCtx)
	req = req.WithContext(ctx)

	cp.Apply(req)

	if got := req.Header.Get("X-User-ID"); got != "" {
		t.Errorf("X-User-ID should be empty without identity, got %q", got)
	}
	if cp.propagated.Load() != 0 {
		t.Errorf("propagated = %d, want 0", cp.propagated.Load())
	}
}

func TestClaimsPropagator_NonStringValues(t *testing.T) {
	cp := New(config.ClaimsPropagationConfig{
		Enabled: true,
		Claims: map[string]string{
			"num":  "X-Num",
			"bool": "X-Bool",
		},
	})

	r := makeRequest(map[string]interface{}{
		"num":  float64(42),
		"bool": true,
	})

	cp.Apply(r)

	if got := r.Header.Get("X-Num"); got != "42" {
		t.Errorf("X-Num = %q, want %q", got, "42")
	}
	if got := r.Header.Get("X-Bool"); got != "true" {
		t.Errorf("X-Bool = %q, want %q", got, "true")
	}
}

func TestExtractClaim(t *testing.T) {
	claims := map[string]interface{}{
		"sub": "user-123",
		"nested": map[string]interface{}{
			"deep": map[string]interface{}{
				"value": "found",
			},
		},
		"nil_val": nil,
	}

	tests := []struct {
		name     string
		expected string
	}{
		{"sub", "user-123"},
		{"nested.deep.value", "found"},
		{"nonexistent", ""},
		{"nested.missing", ""},
		{"nil_val", ""},
	}

	for _, tt := range tests {
		got := extractClaim(claims, tt.name)
		if got != tt.expected {
			t.Errorf("extractClaim(%q) = %q, want %q", tt.name, got, tt.expected)
		}
	}
}

func TestClaimsPropByRoute(t *testing.T) {
	m := NewClaimsPropByRoute()

	m.AddRoute("route1", config.ClaimsPropagationConfig{
		Enabled: true,
		Claims:  map[string]string{"sub": "X-User-ID"},
	})

	if cp := m.GetPropagator("route1"); cp == nil {
		t.Error("expected propagator for route1")
	}
	if cp := m.GetPropagator("nonexistent"); cp != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected RouteIDs: %v", ids)
	}

	stats := m.Stats()
	if len(stats) != 1 {
		t.Errorf("expected 1 stat entry, got %d", len(stats))
	}
}
