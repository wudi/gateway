package geo

import (
	"sync"

	"github.com/wudi/gateway/internal/config"
)

// GeoByRoute manages per-route geo filters.
type GeoByRoute struct {
	mu   sync.RWMutex
	geos map[string]*CompiledGeo
}

// NewGeoByRoute creates a new GeoByRoute manager.
func NewGeoByRoute() *GeoByRoute {
	return &GeoByRoute{
		geos: make(map[string]*CompiledGeo),
	}
}

// AddRoute creates and registers a geo filter for the given route.
func (m *GeoByRoute) AddRoute(routeID string, cfg config.GeoConfig, provider Provider) error {
	if !cfg.Enabled {
		return nil
	}

	g, err := New(routeID, cfg, provider)
	if err != nil {
		return err
	}

	m.mu.Lock()
	m.geos[routeID] = g
	m.mu.Unlock()

	return nil
}

// GetGeo returns the geo filter for a route, or nil.
func (m *GeoByRoute) GetGeo(routeID string) *CompiledGeo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.geos[routeID]
}

// RouteIDs returns all route IDs with geo filters.
func (m *GeoByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.geos))
	for id := range m.geos {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns admin status for all routes.
func (m *GeoByRoute) Stats() map[string]GeoSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]GeoSnapshot, len(m.geos))
	for id, g := range m.geos {
		result[id] = g.Status()
	}
	return result
}
