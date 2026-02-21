package loadbalancer

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware/tenant"
)

func TestTenantAwareBalancer_DefaultWhenNoTenant(t *testing.T) {
	defaultBackends := []*Backend{
		{URL: "http://default:8080", Healthy: true},
	}
	defaultBal := NewRoundRobin(defaultBackends)

	tab := NewTenantAwareBalancer(defaultBal, map[string]Balancer{})

	// No tenant in context → default balancer
	r := httptest.NewRequest("GET", "/", nil)
	backend, _ := tab.NextForHTTPRequest(r)
	if backend == nil {
		t.Fatal("expected a backend")
	}
	if backend.URL != "http://default:8080" {
		t.Errorf("expected default backend, got %s", backend.URL)
	}
}

func TestTenantAwareBalancer_TenantSpecificBackend(t *testing.T) {
	defaultBackends := []*Backend{
		{URL: "http://default:8080", Healthy: true},
	}
	defaultBal := NewRoundRobin(defaultBackends)

	tenantBackends := []*Backend{
		{URL: "http://acme-backend:8080", Healthy: true},
	}
	tenantBal := NewRoundRobin(tenantBackends)

	tab := NewTenantAwareBalancer(defaultBal, map[string]Balancer{
		"acme": tenantBal,
	})

	// Create request with tenant context
	r := httptest.NewRequest("GET", "/", nil)
	info := &tenant.TenantInfo{
		ID:     "acme",
		Config: config.TenantConfig{},
	}
	ctx := tenant.WithContext(r.Context(), info)
	r = r.WithContext(ctx)

	backend, _ := tab.NextForHTTPRequest(r)
	if backend == nil {
		t.Fatal("expected a backend")
	}
	if backend.URL != "http://acme-backend:8080" {
		t.Errorf("expected acme-backend, got %s", backend.URL)
	}
}

func TestTenantAwareBalancer_UnknownTenantUsesDefault(t *testing.T) {
	defaultBackends := []*Backend{
		{URL: "http://default:8080", Healthy: true},
	}
	defaultBal := NewRoundRobin(defaultBackends)

	tab := NewTenantAwareBalancer(defaultBal, map[string]Balancer{
		"acme": NewRoundRobin([]*Backend{{URL: "http://acme:8080", Healthy: true}}),
	})

	// Create request with unknown tenant
	r := httptest.NewRequest("GET", "/", nil)
	info := &tenant.TenantInfo{
		ID:     "unknown-tenant",
		Config: config.TenantConfig{},
	}
	ctx := tenant.WithContext(r.Context(), info)
	r = r.WithContext(ctx)

	backend, _ := tab.NextForHTTPRequest(r)
	if backend == nil {
		t.Fatal("expected a backend")
	}
	if backend.URL != "http://default:8080" {
		t.Errorf("expected default backend for unknown tenant, got %s", backend.URL)
	}
}

func TestTenantAwareBalancer_BalancerInterface(t *testing.T) {
	defaultBackends := []*Backend{
		{URL: "http://default:8080", Weight: 1, Healthy: true},
	}
	defaultBal := NewRoundRobin(defaultBackends)
	tab := NewTenantAwareBalancer(defaultBal, map[string]Balancer{})

	// Test Balancer interface methods
	if tab.Next() == nil {
		t.Error("Next() should return a backend")
	}
	if tab.HealthyCount() != 1 {
		t.Errorf("expected healthy count 1, got %d", tab.HealthyCount())
	}
	if len(tab.GetBackends()) != 1 {
		t.Errorf("expected 1 backend, got %d", len(tab.GetBackends()))
	}
}

func TestTenantAwareBalancer_HealthPropagation(t *testing.T) {
	defaultBackends := []*Backend{
		{URL: "http://default:8080", Weight: 1, Healthy: true},
	}
	defaultBal := NewRoundRobin(defaultBackends)

	tenantBackends := []*Backend{
		{URL: "http://shared:8080", Weight: 1, Healthy: true},
	}
	tenantBal := NewRoundRobin(tenantBackends)

	tab := NewTenantAwareBalancer(defaultBal, map[string]Balancer{
		"acme": tenantBal,
	})

	tab.MarkUnhealthy("http://shared:8080")
	if tenantBal.HealthyCount() != 0 {
		t.Error("expected tenant balancer to have 0 healthy after MarkUnhealthy")
	}

	tab.MarkHealthy("http://shared:8080")
	if tenantBal.HealthyCount() != 1 {
		t.Error("expected tenant balancer to have 1 healthy after MarkHealthy")
	}
}

// Ensure the compilation check: TenantAwareBalancer implements RequestAwareBalancer
var _ RequestAwareBalancer = (*TenantAwareBalancer)(nil)
var _ Balancer = (*TenantAwareBalancer)(nil)

// WithContext is a test helper that needs to be added to the tenant package.
// For now, use context.WithValue directly.
func init() {
	// Test compile-time check for interface compliance
	_ = context.Background()
}
