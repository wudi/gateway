package loadbalancer

import (
	"sync/atomic"
)

// RoundRobin implements round-robin load balancing
type RoundRobin struct {
	baseBalancer
	current uint64
}

// NewRoundRobin creates a new round-robin balancer
func NewRoundRobin(backends []*Backend) *RoundRobin {
	rr := &RoundRobin{
		current: 0,
	}

	// Initialize backends - set default weight if not set
	// Healthy status is preserved as-is from the input
	for _, b := range backends {
		if b.Weight == 0 {
			b.Weight = 1
		}
	}

	rr.backends = backends
	rr.buildIndex()
	return rr
}

// Next returns the next healthy backend using round-robin.
// Uses the pre-computed healthy cache for lock-free reads on the hot path.
func (rr *RoundRobin) Next() *Backend {
	healthy := rr.CachedHealthyBackends()
	if len(healthy) == 0 {
		return nil
	}

	// Atomic increment and modulo for thread-safe round-robin
	idx := atomic.AddUint64(&rr.current, 1)
	return healthy[(idx-1)%uint64(len(healthy))]
}

// WeightedRoundRobin implements weighted round-robin load balancing
type WeightedRoundRobin struct {
	baseBalancer
	current int
	gcd     int
	maxWeight int
}

// NewWeightedRoundRobin creates a new weighted round-robin balancer
func NewWeightedRoundRobin(backends []*Backend) *WeightedRoundRobin {
	wrr := &WeightedRoundRobin{
		current: -1,
	}

	// Initialize backends - set default weight if not set
	// Healthy status is preserved as-is from the input
	for _, b := range backends {
		if b.Weight == 0 {
			b.Weight = 1
		}
	}

	wrr.backends = backends
	wrr.buildIndex()
	wrr.calculateGCD()
	return wrr
}

// calculateGCD calculates GCD of all weights
func (wrr *WeightedRoundRobin) calculateGCD() {
	if len(wrr.backends) == 0 {
		wrr.gcd = 1
		wrr.maxWeight = 0
		return
	}

	wrr.gcd = wrr.backends[0].Weight
	wrr.maxWeight = wrr.backends[0].Weight

	for _, b := range wrr.backends[1:] {
		wrr.gcd = gcd(wrr.gcd, b.Weight)
		if b.Weight > wrr.maxWeight {
			wrr.maxWeight = b.Weight
		}
	}
}

// gcd calculates the greatest common divisor
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// Next returns the next backend using weighted round-robin
func (wrr *WeightedRoundRobin) Next() *Backend {
	wrr.mu.Lock()
	defer wrr.mu.Unlock()

	healthy := wrr.CachedHealthyBackends()
	if len(healthy) == 0 {
		return nil
	}

	// Recalculate weights for healthy backends
	maxWeight := 0
	gcdWeight := healthy[0].Weight
	for _, b := range healthy {
		if b.Weight > maxWeight {
			maxWeight = b.Weight
		}
		gcdWeight = gcd(gcdWeight, b.Weight)
	}

	// Standard weighted round-robin algorithm
	for {
		wrr.current = (wrr.current + 1) % len(healthy)
		if wrr.current == 0 {
			wrr.maxWeight = wrr.maxWeight - gcdWeight
			if wrr.maxWeight <= 0 {
				wrr.maxWeight = maxWeight
			}
		}
		if healthy[wrr.current].Weight >= wrr.maxWeight {
			return healthy[wrr.current]
		}
	}
}

// UpdateBackends updates backends and recalculates GCD
func (wrr *WeightedRoundRobin) UpdateBackends(backends []*Backend) {
	wrr.baseBalancer.UpdateBackends(backends)
	wrr.mu.Lock()
	wrr.calculateGCD()
	wrr.current = -1
	wrr.mu.Unlock()
}
