package versioning

import (
	"sync"

	"github.com/example/gateway/internal/config"
)

// VersioningByRoute manages per-route versioners.
type VersioningByRoute struct {
	versioners map[string]*Versioner
	mu         sync.RWMutex
}

// NewVersioningByRoute creates a new manager.
func NewVersioningByRoute() *VersioningByRoute {
	return &VersioningByRoute{
		versioners: make(map[string]*Versioner),
	}
}

// AddRoute adds a versioner for the given route.
func (m *VersioningByRoute) AddRoute(routeID string, cfg config.VersioningConfig) error {
	v, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.versioners[routeID] = v
	m.mu.Unlock()
	return nil
}

// GetVersioner returns the versioner for a route (nil if none).
func (m *VersioningByRoute) GetVersioner(routeID string) *Versioner {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.versioners[routeID]
}

// RouteIDs returns all route IDs with versioning configured.
func (m *VersioningByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.versioners))
	for id := range m.versioners {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns snapshots for all routes.
func (m *VersioningByRoute) Stats() map[string]VersioningSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]VersioningSnapshot, len(m.versioners))
	for id, v := range m.versioners {
		result[id] = v.Snapshot()
	}
	return result
}
