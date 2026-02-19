package geo

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// GeoByRoute manages per-route geo filters.
type GeoByRoute struct {
	byroute.Manager[*CompiledGeo]
}

// NewGeoByRoute creates a new GeoByRoute manager.
func NewGeoByRoute() *GeoByRoute {
	return &GeoByRoute{}
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

	m.Add(routeID, g)

	return nil
}

// GetGeo returns the geo filter for a route, or nil.
func (m *GeoByRoute) GetGeo(routeID string) *CompiledGeo {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns admin status for all routes.
func (m *GeoByRoute) Stats() map[string]GeoSnapshot {
	result := make(map[string]GeoSnapshot)
	m.Range(func(id string, g *CompiledGeo) bool {
		result[id] = g.Status()
		return true
	})
	return result
}
