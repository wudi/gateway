package trafficshape

// ThrottleSnapshot contains point-in-time throttle metrics.
type ThrottleSnapshot struct {
	TotalRequests  int64   `json:"total_requests"`
	TotalThrottled int64   `json:"total_throttled"`
	TotalTimedOut  int64   `json:"total_timed_out"`
	AvgWaitMs      float64 `json:"avg_wait_ms"`
}

// BandwidthSnapshot contains point-in-time bandwidth metrics.
type BandwidthSnapshot struct {
	RequestRateBPS     int64 `json:"request_rate_bps"`
	ResponseRateBPS    int64 `json:"response_rate_bps"`
	TotalRequestBytes  int64 `json:"total_request_bytes"`
	TotalResponseBytes int64 `json:"total_response_bytes"`
}

// PrioritySnapshot contains point-in-time priority metrics.
type PrioritySnapshot struct {
	MaxConcurrent int   `json:"max_concurrent"`
	Active        int   `json:"active"`
	QueueDepth    int   `json:"queue_depth"`
	TotalAdmitted int64 `json:"total_admitted"`
	TotalRejected int64 `json:"total_rejected"`
}
