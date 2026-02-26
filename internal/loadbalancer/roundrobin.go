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
	current        int
	gcd            int
	maxWeight      int
	healthyGCD      int          // cached GCD of healthy backends
	healthyMaxW     int          // cached max weight of healthy backends
	healthySnap     []*Backend   // last-seen healthy slice (compared by header)
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

	// Recompute GCD/max only when the healthy set changes.
	// Compare slice header (pointer + length) to detect changes cheaply.
	if len(healthy) != len(wrr.healthySnap) ||
		(len(healthy) > 0 && &healthy[0] != &wrr.healthySnap[0]) {
		wrr.healthyGCD = healthy[0].Weight
		wrr.healthyMaxW = healthy[0].Weight
		for _, b := range healthy[1:] {
			wrr.healthyGCD = gcd(wrr.healthyGCD, b.Weight)
			if b.Weight > wrr.healthyMaxW {
				wrr.healthyMaxW = b.Weight
			}
		}
		wrr.healthySnap = healthy
		wrr.current = -1
		wrr.maxWeight = wrr.healthyMaxW
	}

	// Standard weighted round-robin algorithm
	for {
		wrr.current = (wrr.current + 1) % len(healthy)
		if wrr.current == 0 {
			wrr.maxWeight = wrr.maxWeight - wrr.healthyGCD
			if wrr.maxWeight <= 0 {
				wrr.maxWeight = wrr.healthyMaxW
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
	wrr.healthySnap = nil // force recompute on next call
	wrr.mu.Unlock()
}
