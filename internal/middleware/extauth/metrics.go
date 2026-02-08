package extauth

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const latencyRingSize = 1000

// ExtAuthMetrics tracks metrics for external auth calls.
type ExtAuthMetrics struct {
	Total     atomic.Int64
	Allowed   atomic.Int64
	Denied    atomic.Int64
	Errors    atomic.Int64
	CacheHits atomic.Int64

	latencies []time.Duration
	latIdx    int
	latMu     sync.Mutex
}

// NewExtAuthMetrics creates a new ExtAuthMetrics.
func NewExtAuthMetrics() *ExtAuthMetrics {
	return &ExtAuthMetrics{
		latencies: make([]time.Duration, 0, latencyRingSize),
	}
}

// Record records an auth check result with its latency.
func (m *ExtAuthMetrics) Record(allowed bool, latency time.Duration) {
	m.Total.Add(1)
	if allowed {
		m.Allowed.Add(1)
	} else {
		m.Denied.Add(1)
	}
	m.addLatency(latency)
}

// RecordError records an auth check error.
func (m *ExtAuthMetrics) RecordError() {
	m.Total.Add(1)
	m.Errors.Add(1)
}

// RecordCacheHit records a cache hit.
func (m *ExtAuthMetrics) RecordCacheHit() {
	m.CacheHits.Add(1)
}

func (m *ExtAuthMetrics) addLatency(d time.Duration) {
	m.latMu.Lock()
	defer m.latMu.Unlock()

	if len(m.latencies) < latencyRingSize {
		m.latencies = append(m.latencies, d)
	} else {
		m.latencies[m.latIdx] = d
	}
	m.latIdx = (m.latIdx + 1) % latencyRingSize
}

// ExtAuthSnapshot is a point-in-time summary of ext auth metrics.
type ExtAuthSnapshot struct {
	Total     int64         `json:"total"`
	Allowed   int64         `json:"allowed"`
	Denied    int64         `json:"denied"`
	Errors    int64         `json:"errors"`
	CacheHits int64         `json:"cache_hits"`
	LatencyP50 time.Duration `json:"latency_p50_ms"`
	LatencyP95 time.Duration `json:"latency_p95_ms"`
	LatencyP99 time.Duration `json:"latency_p99_ms"`
}

// Snapshot returns a point-in-time summary.
func (m *ExtAuthMetrics) Snapshot() ExtAuthSnapshot {
	snap := ExtAuthSnapshot{
		Total:     m.Total.Load(),
		Allowed:   m.Allowed.Load(),
		Denied:    m.Denied.Load(),
		Errors:    m.Errors.Load(),
		CacheHits: m.CacheHits.Load(),
	}

	m.latMu.Lock()
	n := len(m.latencies)
	if n > 0 {
		sorted := make([]time.Duration, n)
		copy(sorted, m.latencies)
		m.latMu.Unlock()

		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		snap.LatencyP50 = sorted[percentileIdx(n, 50)]
		snap.LatencyP95 = sorted[percentileIdx(n, 95)]
		snap.LatencyP99 = sorted[percentileIdx(n, 99)]
	} else {
		m.latMu.Unlock()
	}

	return snap
}

func percentileIdx(n, p int) int {
	idx := (n * p / 100)
	if idx >= n {
		idx = n - 1
	}
	return idx
}
