package versioning

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
)

// VersioningByRoute manages per-route versioners.
type VersioningByRoute struct {
	byroute.Manager[*Versioner]
}

// NewVersioningByRoute creates a new manager.
func NewVersioningByRoute() *VersioningByRoute {
	return &VersioningByRoute{}
}

// AddRoute adds a versioner for the given route.
func (m *VersioningByRoute) AddRoute(routeID string, cfg config.VersioningConfig) error {
	v, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, v)
	return nil
}

// GetVersioner returns the versioner for a route (nil if none).
func (m *VersioningByRoute) GetVersioner(routeID string) *Versioner {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *VersioningByRoute) Stats() map[string]VersioningSnapshot {
	return byroute.CollectStats(&m.Manager, func(v *Versioner) VersioningSnapshot { return v.Snapshot() })
}
