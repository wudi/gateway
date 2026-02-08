package canary

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// GroupMetrics tracks per-traffic-group request outcomes.
type GroupMetrics struct {
	requests  atomic.Int64
	errors    atomic.Int64 // 5xx responses
	latencies *LatencyRing
}

// NewGroupMetrics creates a new GroupMetrics.
func NewGroupMetrics() *GroupMetrics {
	return &GroupMetrics{
		latencies: NewLatencyRing(1000),
	}
}

// Record records a request outcome.
func (gm *GroupMetrics) Record(statusCode int, latency time.Duration) {
	gm.requests.Add(1)
	if statusCode >= 500 {
		gm.errors.Add(1)
	}
	gm.latencies.Add(latency)
}

// ErrorRate returns the current error rate (0.0-1.0).
func (gm *GroupMetrics) ErrorRate() float64 {
	total := gm.requests.Load()
	if total == 0 {
		return 0
	}
	return float64(gm.errors.Load()) / float64(total)
}

// Requests returns the total request count.
func (gm *GroupMetrics) Requests() int64 {
	return gm.requests.Load()
}

// P99 returns the p99 latency.
func (gm *GroupMetrics) P99() time.Duration {
	return gm.latencies.P99()
}

// Reset clears all metrics (called on step advance for fresh evaluation).
func (gm *GroupMetrics) Reset() {
	gm.requests.Store(0)
	gm.errors.Store(0)
	gm.latencies.Reset()
}

// Snapshot returns a JSON-serializable view of the metrics.
func (gm *GroupMetrics) Snapshot() GroupSnapshot {
	total := gm.requests.Load()
	errs := gm.errors.Load()
	var errRate float64
	if total > 0 {
		errRate = float64(errs) / float64(total)
	}
	return GroupSnapshot{
		Requests:    total,
		Errors:      errs,
		ErrorRate:   errRate,
		LatencyP99Ms: float64(gm.latencies.P99().Microseconds()) / 1000.0,
	}
}

// GroupSnapshot is a JSON-serializable view of group metrics.
type GroupSnapshot struct {
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	ErrorRate    float64 `json:"error_rate"`
	LatencyP99Ms float64 `json:"latency_p99_ms"`
}

// LatencyRing is a fixed-capacity circular buffer for latency samples.
type LatencyRing struct {
	samples []time.Duration
	cap     int
	pos     int
	count   int
	mu      sync.Mutex
}

// NewLatencyRing creates a new LatencyRing with the given capacity.
func NewLatencyRing(capacity int) *LatencyRing {
	return &LatencyRing{
		samples: make([]time.Duration, capacity),
		cap:     capacity,
	}
}

// Add appends a latency sample.
func (lr *LatencyRing) Add(d time.Duration) {
	lr.mu.Lock()
	lr.samples[lr.pos] = d
	lr.pos = (lr.pos + 1) % lr.cap
	if lr.count < lr.cap {
		lr.count++
	}
	lr.mu.Unlock()
}

// P99 returns the 99th percentile latency.
func (lr *LatencyRing) P99() time.Duration {
	lr.mu.Lock()
	if lr.count == 0 {
		lr.mu.Unlock()
		return 0
	}
	// Copy active samples
	cp := make([]time.Duration, lr.count)
	copy(cp, lr.samples[:lr.count])
	lr.mu.Unlock()

	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)) * 0.99)
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

// Reset clears all samples.
func (lr *LatencyRing) Reset() {
	lr.mu.Lock()
	lr.pos = 0
	lr.count = 0
	lr.mu.Unlock()
}
