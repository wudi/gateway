package loadbalancer

import (
	"net/url"
	"sync"
	"sync/atomic"
)

// Backend represents a backend server
type Backend struct {
	URL            string
	Weight         int
	Healthy        bool
	ActiveRequests int64
	ParsedURL      *url.URL // pre-parsed URL to avoid per-request parsing
}

// InitParsedURL pre-parses the backend URL for use in the proxy hot path.
// Errors are silently ignored; the proxy falls back to url.Parse if ParsedURL is nil.
func (b *Backend) InitParsedURL() {
	b.ParsedURL, _ = url.Parse(b.URL)
}

// IncrActive atomically increments the active request count.
func (b *Backend) IncrActive() { atomic.AddInt64(&b.ActiveRequests, 1) }

// DecrActive atomically decrements the active request count.
func (b *Backend) DecrActive() { atomic.AddInt64(&b.ActiveRequests, -1) }

// GetActive atomically reads the active request count.
func (b *Backend) GetActive() int64 { return atomic.LoadInt64(&b.ActiveRequests) }

// Balancer is the interface for load balancers
type Balancer interface {
	// Next returns the next backend to use
	Next() *Backend
	// UpdateBackends updates the list of backends
	UpdateBackends(backends []*Backend)
	// MarkHealthy marks a backend as healthy
	MarkHealthy(url string)
	// MarkUnhealthy marks a backend as unhealthy
	MarkUnhealthy(url string)
	// GetBackends returns all backends
	GetBackends() []*Backend
	// HealthyCount returns the number of healthy backends
	HealthyCount() int
}

// baseBalancer provides common functionality for balancers
type baseBalancer struct {
	backends []*Backend
	mu       sync.RWMutex
}

// UpdateBackends updates the list of backends
func (b *baseBalancer) UpdateBackends(backends []*Backend) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Preserve health status for existing backends
	healthStatus := make(map[string]bool)
	for _, backend := range b.backends {
		healthStatus[backend.URL] = backend.Healthy
	}

	b.backends = backends
	for _, backend := range b.backends {
		if healthy, exists := healthStatus[backend.URL]; exists {
			backend.Healthy = healthy
		} else {
			// New backends start as healthy
			backend.Healthy = true
		}
	}
}

// MarkHealthy marks a backend as healthy
func (b *baseBalancer) MarkHealthy(url string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, backend := range b.backends {
		if backend.URL == url {
			backend.Healthy = true
			return
		}
	}
}

// MarkUnhealthy marks a backend as unhealthy
func (b *baseBalancer) MarkUnhealthy(url string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, backend := range b.backends {
		if backend.URL == url {
			backend.Healthy = false
			return
		}
	}
}

// GetBackends returns a copy of all backends
func (b *baseBalancer) GetBackends() []*Backend {
	b.mu.RLock()
	defer b.mu.RUnlock()

	result := make([]*Backend, len(b.backends))
	for i, backend := range b.backends {
		result[i] = &Backend{
			URL:            backend.URL,
			Weight:         backend.Weight,
			Healthy:        backend.Healthy,
			ActiveRequests: atomic.LoadInt64(&backend.ActiveRequests),
		}
	}
	return result
}

// HealthyCount returns the number of healthy backends
func (b *baseBalancer) HealthyCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()

	count := 0
	for _, backend := range b.backends {
		if backend.Healthy {
			count++
		}
	}
	return count
}

// healthyBackends returns a slice of healthy backends (caller must hold lock)
func (b *baseBalancer) healthyBackends() []*Backend {
	healthy := make([]*Backend, 0, len(b.backends))
	for _, backend := range b.backends {
		if backend.Healthy {
			healthy = append(healthy, backend)
		}
	}
	return healthy
}
