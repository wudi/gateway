package loadbalancer

import (
	"sync/atomic"
)

// LeastConnections implements least-connections load balancing.
// It picks the healthy backend with the fewest active requests.
type LeastConnections struct {
	baseBalancer
}

// NewLeastConnections creates a new least-connections balancer.
func NewLeastConnections(backends []*Backend) *LeastConnections {
	lc := &LeastConnections{}
	for _, b := range backends {
		if b.Weight == 0 {
			b.Weight = 1
		}
	}
	lc.backends = backends
	return lc
}

// Next returns the healthy backend with the lowest active request count.
// Ties are broken by slice order.
func (lc *LeastConnections) Next() *Backend {
	lc.mu.RLock()
	healthy := lc.healthyBackends()
	lc.mu.RUnlock()

	if len(healthy) == 0 {
		return nil
	}

	best := healthy[0]
	bestActive := atomic.LoadInt64(&best.ActiveRequests)

	for _, b := range healthy[1:] {
		active := atomic.LoadInt64(&b.ActiveRequests)
		if active < bestActive {
			best = b
			bestActive = active
		}
	}

	return best
}
