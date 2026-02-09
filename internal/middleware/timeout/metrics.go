package timeout

import "sync/atomic"

// TimeoutMetrics tracks timeout-related counters for a route.
type TimeoutMetrics struct {
	TotalRequests   atomic.Int64
	RequestTimeouts atomic.Int64
	BackendTimeouts atomic.Int64
}

// TimeoutSnapshot is a point-in-time snapshot of timeout metrics.
type TimeoutSnapshot struct {
	TotalRequests   int64 `json:"total_requests"`
	RequestTimeouts int64 `json:"request_timeouts"`
	BackendTimeouts int64 `json:"backend_timeouts"`
}

// Snapshot returns a copy of the current metrics.
func (m *TimeoutMetrics) Snapshot() TimeoutSnapshot {
	return TimeoutSnapshot{
		TotalRequests:   m.TotalRequests.Load(),
		RequestTimeouts: m.RequestTimeouts.Load(),
		BackendTimeouts: m.BackendTimeouts.Load(),
	}
}
