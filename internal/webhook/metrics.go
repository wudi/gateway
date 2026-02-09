package webhook

import "sync/atomic"

// Metrics tracks webhook delivery statistics.
type Metrics struct {
	TotalEmitted   atomic.Int64
	TotalDelivered atomic.Int64
	TotalFailed    atomic.Int64
	TotalDropped   atomic.Int64
	TotalRetries   atomic.Int64
}

// MetricsSnapshot is a point-in-time view of delivery metrics.
type MetricsSnapshot struct {
	TotalEmitted   int64 `json:"total_emitted"`
	TotalDelivered int64 `json:"total_delivered"`
	TotalFailed    int64 `json:"total_failed"`
	TotalDropped   int64 `json:"total_dropped"`
	TotalRetries   int64 `json:"total_retries"`
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		TotalEmitted:   m.TotalEmitted.Load(),
		TotalDelivered: m.TotalDelivered.Load(),
		TotalFailed:    m.TotalFailed.Load(),
		TotalDropped:   m.TotalDropped.Load(),
		TotalRetries:   m.TotalRetries.Load(),
	}
}

// DispatcherStats is the admin API view of the webhook dispatcher.
type DispatcherStats struct {
	Enabled      bool            `json:"enabled"`
	Endpoints    int             `json:"endpoints"`
	QueueSize    int             `json:"queue_size"`
	QueueUsed    int             `json:"queue_used"`
	Metrics      MetricsSnapshot `json:"metrics"`
	RecentEvents []Event         `json:"recent_events"`
}
