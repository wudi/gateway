package loadbalancer

import (
	"math"
	"sync"
	"time"
)

// LatencyRecorder is the interface that proxy uses to report backend latency.
type LatencyRecorder interface {
	RecordLatency(url string, d time.Duration)
}

// ewmaLatency tracks exponentially weighted moving average latency.
type ewmaLatency struct {
	mu      sync.Mutex
	value   float64
	samples int
	alpha   float64
}

func newEWMA(alpha float64) *ewmaLatency {
	return &ewmaLatency{alpha: alpha}
}

func (e *ewmaLatency) update(d time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ms := float64(d) / float64(time.Millisecond)
	if e.samples == 0 {
		e.value = ms
	} else {
		e.value = e.alpha*ms + (1-e.alpha)*e.value
	}
	e.samples++
}

func (e *ewmaLatency) get() (float64, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.value, e.samples
}

// LeastResponseTime implements least-response-time load balancing using EWMA latency.
// Backends with no samples (cold start) are preferred.
type LeastResponseTime struct {
	baseBalancer
	latencies map[string]*ewmaLatency
	latMu     sync.RWMutex
	alpha     float64
}

// NewLeastResponseTime creates a new least-response-time balancer.
func NewLeastResponseTime(backends []*Backend) *LeastResponseTime {
	lrt := &LeastResponseTime{
		latencies: make(map[string]*ewmaLatency),
		alpha:     0.5,
	}
	for _, b := range backends {
		if b.Weight == 0 {
			b.Weight = 1
		}
		lrt.latencies[b.URL] = newEWMA(lrt.alpha)
	}
	lrt.backends = backends
	lrt.buildIndex()
	return lrt
}

// RecordLatency records a response time observation for a backend.
func (lrt *LeastResponseTime) RecordLatency(url string, d time.Duration) {
	lrt.latMu.RLock()
	e, ok := lrt.latencies[url]
	lrt.latMu.RUnlock()
	if ok {
		e.update(d)
	}
}

// Next returns the healthy backend with the lowest EWMA latency.
// Backends with no samples are preferred (cold start exploration).
func (lrt *LeastResponseTime) Next() *Backend {
	healthy := lrt.CachedHealthyBackends()
	if len(healthy) == 0 {
		return nil
	}

	lrt.latMu.RLock()
	defer lrt.latMu.RUnlock()

	var best *Backend
	bestLatency := math.MaxFloat64
	bestCold := false

	for _, b := range healthy {
		e, ok := lrt.latencies[b.URL]
		if !ok {
			// No tracker = cold start, prefer immediately
			return b
		}
		lat, samples := e.get()
		cold := samples == 0
		if cold && !bestCold {
			// Cold start backend preferred over any warm backend
			best = b
			bestLatency = lat
			bestCold = true
			continue
		}
		if bestCold && !cold {
			continue
		}
		if lat < bestLatency {
			best = b
			bestLatency = lat
		}
	}

	return best
}

// GetLatencies returns a snapshot of EWMA latencies per backend URL.
func (lrt *LeastResponseTime) GetLatencies() map[string]float64 {
	lrt.latMu.RLock()
	defer lrt.latMu.RUnlock()

	result := make(map[string]float64, len(lrt.latencies))
	for url, e := range lrt.latencies {
		lat, _ := e.get()
		result[url] = lat
	}
	return result
}

// UpdateBackends updates backends and adds/removes latency trackers.
func (lrt *LeastResponseTime) UpdateBackends(backends []*Backend) {
	lrt.baseBalancer.UpdateBackends(backends)

	lrt.latMu.Lock()
	defer lrt.latMu.Unlock()

	// Add trackers for new backends
	newSet := make(map[string]bool, len(backends))
	for _, b := range backends {
		newSet[b.URL] = true
		if _, ok := lrt.latencies[b.URL]; !ok {
			lrt.latencies[b.URL] = newEWMA(lrt.alpha)
		}
	}
	// Remove trackers for removed backends
	for url := range lrt.latencies {
		if !newSet[url] {
			delete(lrt.latencies, url)
		}
	}
}
