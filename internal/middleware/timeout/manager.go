package timeout

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// TimeoutStatus describes the timeout configuration and metrics for a route.
type TimeoutStatus struct {
	Request       string          `json:"request,omitempty"`
	Idle          string          `json:"idle,omitempty"`
	Backend       string          `json:"backend,omitempty"`
	HeaderTimeout string          `json:"header_timeout,omitempty"`
	Metrics       TimeoutSnapshot `json:"metrics"`
}

// TimeoutByRoute manages per-route compiled timeouts.
type TimeoutByRoute struct {
	byroute.Manager[*CompiledTimeout]
}

// NewTimeoutByRoute creates a new timeout manager.
func NewTimeoutByRoute() *TimeoutByRoute {
	return &TimeoutByRoute{}
}

// AddRoute registers a compiled timeout for the given route.
func (m *TimeoutByRoute) AddRoute(routeID string, cfg config.TimeoutConfig) {
	m.Add(routeID, New(cfg))
}

// GetTimeout returns the compiled timeout for a route, or nil if none configured.
func (m *TimeoutByRoute) GetTimeout(routeID string) *CompiledTimeout {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns timeout status for all routes.
func (m *TimeoutByRoute) Stats() map[string]TimeoutStatus {
	return byroute.CollectStats(&m.Manager, func(ct *CompiledTimeout) TimeoutStatus {
		s := TimeoutStatus{Metrics: ct.Metrics()}
		if ct.Request > 0 {
			s.Request = ct.Request.String()
		}
		if ct.Idle > 0 {
			s.Idle = ct.Idle.String()
		}
		if ct.Backend > 0 {
			s.Backend = ct.Backend.String()
		}
		if ct.HeaderTimeout > 0 {
			s.HeaderTimeout = ct.HeaderTimeout.String()
		}
		return s
	})
}
