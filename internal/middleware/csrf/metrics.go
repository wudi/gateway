package csrf

import "sync/atomic"

// CSRFMetrics tracks CSRF check outcomes using atomic counters.
type CSRFMetrics struct {
	TotalRequests     atomic.Int64
	TokenGenerated    atomic.Int64
	ValidationSuccess atomic.Int64
	ValidationFailed  atomic.Int64
	OriginCheckFailed atomic.Int64
	MissingToken      atomic.Int64
	ExpiredToken      atomic.Int64
	InvalidSignature  atomic.Int64
}

// CSRFStatus is the admin API representation of a CSRF protector's state.
type CSRFStatus struct {
	CookieName        string `json:"cookie_name"`
	HeaderName        string `json:"header_name"`
	TokenTTL          string `json:"token_ttl"`
	ShadowMode        bool   `json:"shadow_mode"`
	InjectToken       bool   `json:"inject_token"`
	TotalRequests     int64  `json:"total_requests"`
	TokenGenerated    int64  `json:"token_generated"`
	ValidationSuccess int64  `json:"validation_success"`
	ValidationFailed  int64  `json:"validation_failed"`
	OriginCheckFailed int64  `json:"origin_check_failed"`
	MissingToken      int64  `json:"missing_token"`
	ExpiredToken      int64  `json:"expired_token"`
	InvalidSignature  int64  `json:"invalid_signature"`
}
