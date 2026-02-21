package mirror

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const latencyRingSize = 1000

// MirrorMetrics tracks metrics for mirrored traffic.
type MirrorMetrics struct {
	TotalMirrored  atomic.Int64
	TotalErrors    atomic.Int64
	TotalCompared  atomic.Int64
	TotalMismatches atomic.Int64

	StatusMismatches atomic.Int64
	HeaderMismatches atomic.Int64
	BodyMismatches   atomic.Int64

	latencies []time.Duration
	latIdx    int
	latMu     sync.Mutex
}

// NewMirrorMetrics creates a new MirrorMetrics.
func NewMirrorMetrics() *MirrorMetrics {
	return &MirrorMetrics{
		latencies: make([]time.Duration, 0, latencyRingSize),
	}
}

// RecordSuccess records a successful mirrored request with its latency.
func (m *MirrorMetrics) RecordSuccess(latency time.Duration) {
	m.TotalMirrored.Add(1)
	m.addLatency(latency)
}

// RecordError records a failed mirrored request.
func (m *MirrorMetrics) RecordError() {
	m.TotalMirrored.Add(1)
	m.TotalErrors.Add(1)
}

// RecordComparison records a comparison result.
func (m *MirrorMetrics) RecordComparison(result CompareResult) {
	m.TotalCompared.Add(1)
	if !result.StatusMatch || !result.BodyMatch {
		m.TotalMismatches.Add(1)
	}
}

// RecordDetailedComparison records a detailed comparison result with per-type counters.
func (m *MirrorMetrics) RecordDetailedComparison(result CompareResult, detail *DiffDetail) {
	m.TotalCompared.Add(1)
	if detail == nil || !detail.HasDiffs() {
		return
	}
	m.TotalMismatches.Add(1)
	if detail.StatusDiff != nil {
		m.StatusMismatches.Add(1)
	}
	if len(detail.HeaderDiffs) > 0 {
		m.HeaderMismatches.Add(1)
	}
	if len(detail.BodyDiffs) > 0 {
		m.BodyMismatches.Add(1)
	}
}

func (m *MirrorMetrics) addLatency(d time.Duration) {
	m.latMu.Lock()
	defer m.latMu.Unlock()

	if len(m.latencies) < latencyRingSize {
		m.latencies = append(m.latencies, d)
	} else {
		m.latencies[m.latIdx] = d
	}
	m.latIdx = (m.latIdx + 1) % latencyRingSize
}

// MirrorSnapshot is a point-in-time summary of mirror metrics.
type MirrorSnapshot struct {
	TotalMirrored    int64         `json:"total_mirrored"`
	TotalErrors      int64         `json:"total_errors"`
	TotalCompared    int64         `json:"total_compared"`
	TotalMismatches  int64         `json:"total_mismatches"`
	StatusMismatches int64         `json:"status_mismatches"`
	HeaderMismatches int64         `json:"header_mismatches"`
	BodyMismatches   int64         `json:"body_mismatches"`
	MismatchStoreSize int          `json:"mismatch_store_size"`
	LatencyP50       time.Duration `json:"latency_p50_ms"`
	LatencyP95       time.Duration `json:"latency_p95_ms"`
	LatencyP99       time.Duration `json:"latency_p99_ms"`
}

// Snapshot returns a point-in-time summary of mirror metrics.
func (m *MirrorMetrics) Snapshot() MirrorSnapshot {
	snap := MirrorSnapshot{
		TotalMirrored:    m.TotalMirrored.Load(),
		TotalErrors:      m.TotalErrors.Load(),
		TotalCompared:    m.TotalCompared.Load(),
		TotalMismatches:  m.TotalMismatches.Load(),
		StatusMismatches: m.StatusMismatches.Load(),
		HeaderMismatches: m.HeaderMismatches.Load(),
		BodyMismatches:   m.BodyMismatches.Load(),
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
