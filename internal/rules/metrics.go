package rules

import "sync/atomic"

// Metrics tracks rule evaluation statistics with atomic counters.
type Metrics struct {
	Evaluated    atomic.Int64
	Matched      atomic.Int64
	Blocked      atomic.Int64
	Errors       atomic.Int64
	Logged       atomic.Int64
	ActionCounts map[string]*atomic.Int64 // action_type → count (read-only map, atomic values)
}

// NewMetrics creates a Metrics with pre-initialized action counters.
func NewMetrics() *Metrics {
	m := &Metrics{
		ActionCounts: make(map[string]*atomic.Int64),
	}
	// Pre-initialize counters for all non-terminating action types.
	// The map is read-only after init; only the atomic values are mutated.
	for _, a := range []string{
		"set_headers", "rewrite", "group", "log", "delay", "set_var",
		"cache_bypass", "lua", "set_status", "set_body",
		"skip_auth", "skip_rate_limit", "skip_throttle", "skip_circuit_breaker",
		"skip_waf", "skip_validation", "skip_compression", "skip_adaptive_concurrency",
		"skip_body_limit", "skip_mirror", "skip_access_log", "skip_cache_store",
		"rate_limit_tier", "timeout_override", "priority_override",
		"bandwidth_override", "body_limit_override", "switch_backend",
		"cache_ttl_override",
	} {
		m.ActionCounts[a] = &atomic.Int64{}
	}
	return m
}

// IncrAction increments the counter for the given action type.
func (m *Metrics) IncrAction(actionType string) {
	if c, ok := m.ActionCounts[actionType]; ok {
		c.Add(1)
	}
}

// MetricsSnapshot is a point-in-time copy of Metrics for JSON serialization.
type MetricsSnapshot struct {
	Evaluated    int64            `json:"evaluated"`
	Matched      int64            `json:"matched"`
	Blocked      int64            `json:"blocked"`
	Errors       int64            `json:"errors"`
	Logged       int64            `json:"logged"`
	ActionCounts map[string]int64 `json:"action_counts,omitempty"`
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	snap := MetricsSnapshot{
		Evaluated: m.Evaluated.Load(),
		Matched:   m.Matched.Load(),
		Blocked:   m.Blocked.Load(),
		Errors:    m.Errors.Load(),
		Logged:    m.Logged.Load(),
	}
	if len(m.ActionCounts) > 0 {
		snap.ActionCounts = make(map[string]int64, len(m.ActionCounts))
		for k, v := range m.ActionCounts {
			if n := v.Load(); n > 0 {
				snap.ActionCounts[k] = n
			}
		}
	}
	return snap
}
