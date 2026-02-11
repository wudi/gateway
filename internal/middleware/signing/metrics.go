package signing

import "sync/atomic"

// SigningMetrics tracks per-route signing activity.
type SigningMetrics struct {
	TotalRequests atomic.Int64
	Signed        atomic.Int64
	Errors        atomic.Int64
	BodyHashed    atomic.Int64
}

// SigningStatus is the admin API snapshot for a signer.
type SigningStatus struct {
	RouteID       string `json:"route_id"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	HeaderPrefix  string `json:"header_prefix"`
	IncludeBody   bool   `json:"include_body"`
	TotalRequests int64  `json:"total_requests"`
	Signed        int64  `json:"signed"`
	Errors        int64  `json:"errors"`
	BodyHashed    int64  `json:"body_hashed"`
}
