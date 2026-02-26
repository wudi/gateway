package loadbalancer

import (
	"net/http"
	"sync"

	"github.com/wudi/runway/variables"
)

// VersionedBalancer routes requests to per-version sub-balancers.
type VersionedBalancer struct {
	versions       map[string]*RoundRobin
	defaultVersion string
	mu             sync.RWMutex
}

// NewVersionedBalancer creates a versioned balancer from a map of version -> backends.
func NewVersionedBalancer(versionBackends map[string][]*Backend, defaultVersion string) *VersionedBalancer {
	vb := &VersionedBalancer{
		versions:       make(map[string]*RoundRobin, len(versionBackends)),
		defaultVersion: defaultVersion,
	}
	for ver, backends := range versionBackends {
		vb.versions[ver] = NewRoundRobin(backends)
	}
	return vb
}

// NextForHTTPRequest selects a backend based on the API version in the request context.
// Returns (backend, "") — empty string avoids overwriting varCtx.TrafficGroup.
func (vb *VersionedBalancer) NextForHTTPRequest(r *http.Request) (*Backend, string) {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	version := vb.defaultVersion
	varCtx := variables.GetFromRequest(r)
	if varCtx.APIVersion != "" {
		version = varCtx.APIVersion
	}

	rr, ok := vb.versions[version]
	if !ok {
		// Fall back to default version
		rr, ok = vb.versions[vb.defaultVersion]
		if !ok {
			return nil, ""
		}
	}
	return rr.Next(), ""
}

// Next implements Balancer interface — uses default version.
func (vb *VersionedBalancer) Next() *Backend {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	rr, ok := vb.versions[vb.defaultVersion]
	if !ok {
		return nil
	}
	return rr.Next()
}

// UpdateBackends updates backends for the default version.
func (vb *VersionedBalancer) UpdateBackends(backends []*Backend) {
	vb.mu.Lock()
	defer vb.mu.Unlock()

	if rr, ok := vb.versions[vb.defaultVersion]; ok {
		rr.UpdateBackends(backends)
	}
}

// UpdateVersionBackends updates backends for a specific version.
func (vb *VersionedBalancer) UpdateVersionBackends(version string, backends []*Backend) {
	vb.mu.Lock()
	defer vb.mu.Unlock()

	if rr, ok := vb.versions[version]; ok {
		rr.UpdateBackends(backends)
	}
}

// MarkHealthy marks a backend as healthy across all versions.
func (vb *VersionedBalancer) MarkHealthy(url string) {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	for _, rr := range vb.versions {
		rr.MarkHealthy(url)
	}
}

// MarkUnhealthy marks a backend as unhealthy across all versions.
func (vb *VersionedBalancer) MarkUnhealthy(url string) {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	for _, rr := range vb.versions {
		rr.MarkUnhealthy(url)
	}
}

// GetBackends returns all backends across all versions.
func (vb *VersionedBalancer) GetBackends() []*Backend {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	var all []*Backend
	for _, rr := range vb.versions {
		all = append(all, rr.GetBackends()...)
	}
	return all
}

// GetVersionBackends returns backends for a specific version.
func (vb *VersionedBalancer) GetVersionBackends(version string) []*Backend {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	if rr, ok := vb.versions[version]; ok {
		return rr.GetBackends()
	}
	return nil
}

// HealthyCount returns total healthy backends across all versions.
func (vb *VersionedBalancer) HealthyCount() int {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	count := 0
	for _, rr := range vb.versions {
		count += rr.HealthyCount()
	}
	return count
}

// VersionNames returns the list of configured version names.
func (vb *VersionedBalancer) VersionNames() []string {
	vb.mu.RLock()
	defer vb.mu.RUnlock()

	names := make([]string, 0, len(vb.versions))
	for name := range vb.versions {
		names = append(names, name)
	}
	return names
}
