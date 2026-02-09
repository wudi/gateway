package idempotency

import "sync/atomic"

// IdempotencyMetrics tracks idempotency check outcomes using atomic counters.
type IdempotencyMetrics struct {
	TotalRequests   atomic.Int64
	CacheHits       atomic.Int64
	CacheMisses     atomic.Int64
	InFlightWaits   atomic.Int64
	Enforced        atomic.Int64
	InvalidKey      atomic.Int64
	StoreErrors     atomic.Int64
	ResponsesStored atomic.Int64
}

// IdempotencyStatus is the admin API representation of an idempotency handler's state.
type IdempotencyStatus struct {
	HeaderName      string `json:"header_name"`
	TTL             string `json:"ttl"`
	Enforce         bool   `json:"enforce"`
	KeyScope        string `json:"key_scope"`
	Mode            string `json:"mode"`
	TotalRequests   int64  `json:"total_requests"`
	CacheHits       int64  `json:"cache_hits"`
	CacheMisses     int64  `json:"cache_misses"`
	InFlightWaits   int64  `json:"in_flight_waits"`
	Enforced        int64  `json:"enforced"`
	InvalidKey      int64  `json:"invalid_key"`
	StoreErrors     int64  `json:"store_errors"`
	ResponsesStored int64  `json:"responses_stored"`
}
