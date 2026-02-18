package csrf

import (
	"sync"

	"github.com/wudi/gateway/internal/config"
)

// CSRFByRoute manages per-route CSRF protectors.
type CSRFByRoute struct {
	mu         sync.RWMutex
	protectors map[string]*CompiledCSRF
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

	m.mu.Lock()
	if m.protectors == nil {
		m.protectors = make(map[string]*CompiledCSRF)
	}
	m.protectors[routeID] = cp
	m.mu.Unlock()

	return nil
}

// GetProtector returns the CSRF protector for a route, or nil.
func (m *CSRFByRoute) GetProtector(routeID string) *CompiledCSRF {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.protectors[routeID]
}

// RouteIDs returns all route IDs with CSRF protectors.
func (m *CSRFByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.protectors))
	for id := range m.protectors {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns admin status for all routes.
func (m *CSRFByRoute) Stats() map[string]CSRFStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]CSRFStatus, len(m.protectors))
	for id, cp := range m.protectors {
		result[id] = cp.Status()
	}
	return result
}
