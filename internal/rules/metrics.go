package rules

import "sync/atomic"

// Metrics tracks rule evaluation statistics with atomic counters.
type Metrics struct {
	Evaluated atomic.Int64
	Matched   atomic.Int64
	Blocked   atomic.Int64
	Errors    atomic.Int64
}

// MetricsSnapshot is a point-in-time copy of Metrics for JSON serialization.
type MetricsSnapshot struct {
	Evaluated int64 `json:"evaluated"`
	Matched   int64 `json:"matched"`
	Blocked   int64 `json:"blocked"`
	Errors    int64 `json:"errors"`
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		Evaluated: m.Evaluated.Load(),
		Matched:   m.Matched.Load(),
		Blocked:   m.Blocked.Load(),
		Errors:    m.Errors.Load(),
	}
}
