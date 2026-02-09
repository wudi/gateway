package errorpages

import "sync/atomic"

// ErrorPagesMetrics tracks error page rendering stats.
type ErrorPagesMetrics struct {
	TotalRendered atomic.Int64
}

// ErrorPagesSnapshot is a point-in-time copy of metrics.
type ErrorPagesSnapshot struct {
	TotalRendered int64 `json:"total_rendered"`
}

// ErrorPagesStatus describes the error pages config and metrics for a route.
type ErrorPagesStatus struct {
	PageKeys []string           `json:"page_keys"`
	Metrics  ErrorPagesSnapshot `json:"metrics"`
}
