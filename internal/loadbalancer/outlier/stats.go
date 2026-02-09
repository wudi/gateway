package outlier

import (
	"math"
	"sort"
	"sync"
	"time"
)

const statsBuckets = 10

type bucketData struct {
	requests int64
	errors   int64
	latencies []time.Duration
}

// StatsSnapshot is a point-in-time aggregate of per-backend metrics.
type StatsSnapshot struct {
	TotalRequests int64         `json:"total_requests"`
	TotalErrors   int64         `json:"total_errors"`
	ErrorRate     float64       `json:"error_rate"`
	P50           time.Duration `json:"p50"`
	P99           time.Duration `json:"p99"`
}

// BackendStats tracks sliding-window per-backend metrics using a ring buffer.
type BackendStats struct {
	window    time.Duration
	bucketDur time.Duration

	mu      sync.Mutex
	buckets [statsBuckets]bucketData
	idx     int
	lastAdv time.Time
}

// NewBackendStats creates a new sliding-window stats tracker.
func NewBackendStats(window time.Duration) *BackendStats {
	if window <= 0 {
		window = 30 * time.Second
	}
	return &BackendStats{
		window:    window,
		bucketDur: window / statsBuckets,
		lastAdv:   time.Now(),
	}
}

// Record records a single request outcome.
func (s *BackendStats) Record(statusCode int, latency time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance()
	s.buckets[s.idx].requests++
	if statusCode >= 500 {
		s.buckets[s.idx].errors++
	}
	s.buckets[s.idx].latencies = append(s.buckets[s.idx].latencies, latency)
}

// Snapshot aggregates metrics over the sliding window.
func (s *BackendStats) Snapshot() StatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advance()

	var snap StatsSnapshot
	var allLatencies []time.Duration
	for i := 0; i < statsBuckets; i++ {
		snap.TotalRequests += s.buckets[i].requests
		snap.TotalErrors += s.buckets[i].errors
		allLatencies = append(allLatencies, s.buckets[i].latencies...)
	}

	if snap.TotalRequests > 0 {
		snap.ErrorRate = float64(snap.TotalErrors) / float64(snap.TotalRequests)
	}

	if len(allLatencies) > 0 {
		sort.Slice(allLatencies, func(i, j int) bool { return allLatencies[i] < allLatencies[j] })
		snap.P50 = percentile(allLatencies, 0.50)
		snap.P99 = percentile(allLatencies, 0.99)
	}

	return snap
}

// advance moves the window forward, zeroing expired buckets.
func (s *BackendStats) advance() {
	now := time.Now()
	elapsed := now.Sub(s.lastAdv)
	if elapsed < s.bucketDur {
		return
	}

	steps := int(elapsed / s.bucketDur)
	if steps > statsBuckets {
		steps = statsBuckets
	}
	for i := 0; i < steps; i++ {
		s.idx = (s.idx + 1) % statsBuckets
		s.buckets[s.idx] = bucketData{}
	}
	s.lastAdv = now
}

// percentile returns the value at the given percentile from a sorted slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
