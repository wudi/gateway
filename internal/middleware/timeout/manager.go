package timeout

import (
	"sync"

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
	mu       sync.RWMutex
	timeouts map[string]*CompiledTimeout
}

// NewTimeoutByRoute creates a new timeout manager.
func NewTimeoutByRoute() *TimeoutByRoute {
	return &TimeoutByRoute{}
}

// AddRoute registers a compiled timeout for the given route.
func (m *TimeoutByRoute) AddRoute(routeID string, cfg config.TimeoutConfig) {
	ct := New(cfg)
	m.mu.Lock()
	if m.timeouts == nil {
		m.timeouts = make(map[string]*CompiledTimeout)
	}
	m.timeouts[routeID] = ct
	m.mu.Unlock()
}

// GetTimeout returns the compiled timeout for a route, or nil if none configured.
func (m *TimeoutByRoute) GetTimeout(routeID string) *CompiledTimeout {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.timeouts[routeID]
}

// RouteIDs returns all route IDs that have timeout configuration.
func (m *TimeoutByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.timeouts))
	for id := range m.timeouts {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns timeout status for all routes.
func (m *TimeoutByRoute) Stats() map[string]TimeoutStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]TimeoutStatus, len(m.timeouts))
	for id, ct := range m.timeouts {
		s := TimeoutStatus{
			Metrics: ct.Metrics(),
		}
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
		result[id] = s
	}
	return result
}
