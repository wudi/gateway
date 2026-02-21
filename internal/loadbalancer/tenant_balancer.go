package loadbalancer

import (
	"net/http"

	"github.com/wudi/gateway/internal/middleware/tenant"
)

// TenantAwareBalancer wraps a default balancer with per-tenant backend sets.
// It implements RequestAwareBalancer so the proxy layer automatically uses
// NextForHTTPRequest when available.
type TenantAwareBalancer struct {
	defaultBalancer Balancer
	tenantBalancers map[string]Balancer // tenantID -> dedicated balancer
}

// NewTenantAwareBalancer creates a new tenant-aware balancer.
func NewTenantAwareBalancer(defaultBalancer Balancer, tenantBalancers map[string]Balancer) *TenantAwareBalancer {
	return &TenantAwareBalancer{
		defaultBalancer: defaultBalancer,
		tenantBalancers: tenantBalancers,
	}
}

// Next delegates to the default balancer (used when no request context is available).
func (t *TenantAwareBalancer) Next() *Backend {
	return t.defaultBalancer.Next()
}

// UpdateBackends updates the default balancer's backends.
func (t *TenantAwareBalancer) UpdateBackends(backends []*Backend) {
	t.defaultBalancer.UpdateBackends(backends)
}

// MarkHealthy marks a backend as healthy across all balancers.
func (t *TenantAwareBalancer) MarkHealthy(url string) {
	t.defaultBalancer.MarkHealthy(url)
	for _, b := range t.tenantBalancers {
		b.MarkHealthy(url)
	}
}

// MarkUnhealthy marks a backend as unhealthy across all balancers.
func (t *TenantAwareBalancer) MarkUnhealthy(url string) {
	t.defaultBalancer.MarkUnhealthy(url)
	for _, b := range t.tenantBalancers {
		b.MarkUnhealthy(url)
	}
}

// GetBackends returns the default balancer's backends.
func (t *TenantAwareBalancer) GetBackends() []*Backend {
	return t.defaultBalancer.GetBackends()
}

// HealthyCount returns the default balancer's healthy count.
func (t *TenantAwareBalancer) HealthyCount() int {
	return t.defaultBalancer.HealthyCount()
}

// NextForHTTPRequest implements RequestAwareBalancer.
// It checks for a resolved tenant and delegates to the tenant-specific balancer
// if one exists, otherwise falls through to the default.
func (t *TenantAwareBalancer) NextForHTTPRequest(r *http.Request) (*Backend, string) {
	if ti := tenant.FromContext(r.Context()); ti != nil {
		if b, ok := t.tenantBalancers[ti.ID]; ok {
			backend := b.Next()
			if backend != nil {
				return backend, ""
			}
			// Fall through to default if tenant balancer has no healthy backends
		}
	}

	// Check if default balancer also implements RequestAwareBalancer
	if rab, ok := t.defaultBalancer.(RequestAwareBalancer); ok {
		return rab.NextForHTTPRequest(r)
	}
	return t.defaultBalancer.Next(), ""
}

// GetTenantBalancer returns the balancer for a specific tenant, if configured.
func (t *TenantAwareBalancer) GetTenantBalancer(tenantID string) (Balancer, bool) {
	b, ok := t.tenantBalancers[tenantID]
	return b, ok
}
