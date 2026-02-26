package loadbalancer

import (
	"net/http"
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
	// GetBackendByURL returns the original Backend pointer for a URL, or nil.
	// Used by switch_backend rule action to route to a specific backend
	// while preserving health/connection tracking on the real object.
	GetBackendByURL(url string) *Backend
}

// baseBalancer provides common functionality for balancers
type baseBalancer struct {
	backends      []*Backend
	urlIndex      map[string]int // URL → index in backends for O(1) health mark
	cachedHealthy atomic.Value   // []*Backend — rebuilt on health changes, read lock-free
	mu            sync.RWMutex
}

// buildIndex rebuilds the URL→index map from the current backends slice.
// Caller must hold the write lock.
func (b *baseBalancer) buildIndex() {
	b.urlIndex = make(map[string]int, len(b.backends))
	for i, backend := range b.backends {
		b.urlIndex[backend.URL] = i
	}
	b.rebuildHealthyCache()
}

// rebuildHealthyCache updates the atomic cached healthy slice.
// Caller must hold the write lock (or be called during init).
func (b *baseBalancer) rebuildHealthyCache() {
	healthy := make([]*Backend, 0, len(b.backends))
	for _, be := range b.backends {
		if be.Healthy {
			healthy = append(healthy, be)
		}
	}
	b.cachedHealthy.Store(healthy)
}

// CachedHealthyBackends returns the pre-computed healthy backends slice (lock-free).
func (b *baseBalancer) CachedHealthyBackends() []*Backend {
	if v := b.cachedHealthy.Load(); v != nil {
		return v.([]*Backend)
	}
	return nil
}

// UpdateBackends updates the list of backends
func (b *baseBalancer) UpdateBackends(backends []*Backend) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Preserve health status for existing backends (reuse old index for O(1) lookup)
	if b.urlIndex != nil {
		for _, backend := range backends {
			if idx, ok := b.urlIndex[backend.URL]; ok {
				backend.Healthy = b.backends[idx].Healthy
			} else {
				backend.Healthy = true
			}
		}
	} else {
		for _, backend := range backends {
			backend.Healthy = true
		}
	}

	b.backends = backends
	b.buildIndex()
}

// MarkHealthy marks a backend as healthy
func (b *baseBalancer) MarkHealthy(url string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if idx, ok := b.urlIndex[url]; ok {
		b.backends[idx].Healthy = true
		b.rebuildHealthyCache()
	}
}

// MarkUnhealthy marks a backend as unhealthy
func (b *baseBalancer) MarkUnhealthy(url string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if idx, ok := b.urlIndex[url]; ok {
		b.backends[idx].Healthy = false
		b.rebuildHealthyCache()
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

// GetBackendByURL returns the original Backend pointer for a URL, or nil if not found.
// Returns the real reference so IncrActive/DecrActive update the shared counter.
func (b *baseBalancer) GetBackendByURL(backendURL string) *Backend {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if idx, ok := b.urlIndex[backendURL]; ok {
		return b.backends[idx]
	}
	return nil
}

// RequestAwareBalancer is a balancer that can pick a backend based on the HTTP request
// (e.g., consistent hashing, sticky sessions, versioned routing).
type RequestAwareBalancer interface {
	NextForHTTPRequest(r *http.Request) (*Backend, string)
}

// healthyBackends returns a slice of healthy backends (caller must hold lock).
// Returns the backends slice directly when all are healthy (zero allocations).
func (b *baseBalancer) healthyBackends() []*Backend {
	for _, backend := range b.backends {
		if !backend.Healthy {
			// At least one unhealthy: allocate filtered slice.
			healthy := make([]*Backend, 0, len(b.backends))
			for _, be := range b.backends {
				if be.Healthy {
					healthy = append(healthy, be)
				}
			}
			return healthy
		}
	}
	return b.backends
}
