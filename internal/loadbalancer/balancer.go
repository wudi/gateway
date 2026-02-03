package loadbalancer

import (
	"sync"
)

// Backend represents a backend server
type Backend struct {
	URL     string
	Weight  int
	Healthy bool
}

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
			URL:     backend.URL,
			Weight:  backend.Weight,
			Healthy: backend.Healthy,
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
	var healthy []*Backend
	for _, backend := range b.backends {
		if backend.Healthy {
			healthy = append(healthy, backend)
		}
	}
	return healthy
}
