package loadbalancer

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/example/gateway/internal/variables"
)

func TestVersionedBalancer_Next(t *testing.T) {
	backends := map[string][]*Backend{
		"1": {{URL: "http://v1:8080", Weight: 1, Healthy: true}},
		"2": {{URL: "http://v2:8080", Weight: 1, Healthy: true}},
	}
	vb := NewVersionedBalancer(backends, "1")

	// Next() should use default version
	b := vb.Next()
	if b == nil {
		t.Fatal("expected backend from Next()")
	}
	if b.URL != "http://v1:8080" {
		t.Errorf("Next() = %q, want http://v1:8080", b.URL)
	}
}

func TestVersionedBalancer_NextForHTTPRequest(t *testing.T) {
	backends := map[string][]*Backend{
		"1": {{URL: "http://v1:8080", Weight: 1, Healthy: true}},
		"2": {{URL: "http://v2:8080", Weight: 1, Healthy: true}},
	}
	vb := NewVersionedBalancer(backends, "1")

	// Request with APIVersion = "2"
	r := httptest.NewRequest("GET", "/users", nil)
	varCtx := variables.NewContext(r)
	varCtx.APIVersion = "2"
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	r = r.WithContext(ctx)

	b, group := vb.NextForHTTPRequest(r)
	if b == nil {
		t.Fatal("expected backend")
	}
	if b.URL != "http://v2:8080" {
		t.Errorf("NextForHTTPRequest(v2) = %q, want http://v2:8080", b.URL)
	}
	if group != "" {
		t.Errorf("group = %q, want empty", group)
	}
}

func TestVersionedBalancer_DefaultFallback(t *testing.T) {
	backends := map[string][]*Backend{
		"1": {{URL: "http://v1:8080", Weight: 1, Healthy: true}},
	}
	vb := NewVersionedBalancer(backends, "1")

	// Request with unknown version
	r := httptest.NewRequest("GET", "/users", nil)
	varCtx := variables.NewContext(r)
	varCtx.APIVersion = "99"
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	r = r.WithContext(ctx)

	b, _ := vb.NextForHTTPRequest(r)
	if b == nil {
		t.Fatal("expected fallback to default")
	}
	if b.URL != "http://v1:8080" {
		t.Errorf("fallback = %q, want http://v1:8080", b.URL)
	}
}

func TestVersionedBalancer_GetBackends(t *testing.T) {
	backends := map[string][]*Backend{
		"1": {{URL: "http://v1:8080", Weight: 1, Healthy: true}},
		"2": {{URL: "http://v2:8080", Weight: 1, Healthy: true}},
	}
	vb := NewVersionedBalancer(backends, "1")

	all := vb.GetBackends()
	if len(all) != 2 {
		t.Errorf("GetBackends() count = %d, want 2", len(all))
	}
}

func TestVersionedBalancer_GetVersionBackends(t *testing.T) {
	backends := map[string][]*Backend{
		"1": {{URL: "http://v1:8080", Weight: 1, Healthy: true}},
		"2": {
			{URL: "http://v2a:8080", Weight: 1, Healthy: true},
			{URL: "http://v2b:8080", Weight: 1, Healthy: true},
		},
	}
	vb := NewVersionedBalancer(backends, "1")

	v2 := vb.GetVersionBackends("2")
	if len(v2) != 2 {
		t.Errorf("GetVersionBackends(2) = %d, want 2", len(v2))
	}

	v3 := vb.GetVersionBackends("3")
	if len(v3) != 0 {
		t.Errorf("GetVersionBackends(3) = %d, want 0", len(v3))
	}
}

func TestVersionedBalancer_HealthMarking(t *testing.T) {
	backends := map[string][]*Backend{
		"1": {{URL: "http://v1:8080", Weight: 1, Healthy: true}},
		"2": {{URL: "http://v2:8080", Weight: 1, Healthy: true}},
	}
	vb := NewVersionedBalancer(backends, "1")

	if vb.HealthyCount() != 2 {
		t.Errorf("HealthyCount = %d, want 2", vb.HealthyCount())
	}

	vb.MarkUnhealthy("http://v1:8080")
	if vb.HealthyCount() != 1 {
		t.Errorf("HealthyCount after MarkUnhealthy = %d, want 1", vb.HealthyCount())
	}

	vb.MarkHealthy("http://v1:8080")
	if vb.HealthyCount() != 2 {
		t.Errorf("HealthyCount after MarkHealthy = %d, want 2", vb.HealthyCount())
	}
}

func TestVersionedBalancer_VersionNames(t *testing.T) {
	backends := map[string][]*Backend{
		"1": {{URL: "http://v1:8080", Weight: 1, Healthy: true}},
		"2": {{URL: "http://v2:8080", Weight: 1, Healthy: true}},
	}
	vb := NewVersionedBalancer(backends, "1")

	names := vb.VersionNames()
	if len(names) != 2 {
		t.Errorf("VersionNames() = %d, want 2", len(names))
	}
}
