package csrf

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// CSRFByRoute manages per-route CSRF protectors.
type CSRFByRoute struct {
	byroute.Manager[*CompiledCSRF]
}

// NewCSRFByRoute creates a new CSRFByRoute manager.
func NewCSRFByRoute() *CSRFByRoute {
	return &CSRFByRoute{}
}

// AddRoute creates and registers a CSRF protector for the given route.
func (m *CSRFByRoute) AddRoute(routeID string, cfg config.CSRFConfig) error {
	if !cfg.Enabled {
		return nil
	}

	cp, err := New(routeID, cfg)
	if err != nil {
		return err
	}

	m.Add(routeID, cp)

	return nil
}

// GetProtector returns the CSRF protector for a route, or nil.
func (m *CSRFByRoute) GetProtector(routeID string) *CompiledCSRF {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns admin status for all routes.
func (m *CSRFByRoute) Stats() map[string]CSRFStatus {
	result := make(map[string]CSRFStatus)
	m.Range(func(id string, cp *CompiledCSRF) bool {
		result[id] = cp.Status()
		return true
	})
	return result
}
