package nonce

import "sync/atomic"

// NonceMetrics tracks nonce check outcomes using atomic counters.
type NonceMetrics struct {
	TotalChecked   atomic.Int64
	Rejected       atomic.Int64
	MissingNonce   atomic.Int64
	StaleTimestamp atomic.Int64
	StoreSize      func() int
}

// NonceStatus is the admin API representation of a nonce checker's state.
type NonceStatus struct {
	Header         string `json:"header"`
	QueryParam     string `json:"query_param,omitempty"`
	Mode           string `json:"mode"`
	Scope          string `json:"scope"`
	TTL            string `json:"ttl"`
	Required       bool   `json:"required"`
	TotalChecked   int64  `json:"total_checked"`
	Rejected       int64  `json:"rejected"`
	MissingNonce   int64  `json:"missing_nonce"`
	StaleTimestamp int64  `json:"stale_timestamp"`
	StoreSize      int    `json:"store_size"`
}
