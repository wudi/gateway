package extauth

import (
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// ExtAuthByRoute manages per-route external auth clients.
type ExtAuthByRoute struct {
	byroute.Manager[*ExtAuth]
}

// NewExtAuthByRoute creates a new per-route ext auth manager.
func NewExtAuthByRoute() *ExtAuthByRoute {
	return &ExtAuthByRoute{}
}

// AddRoute adds an ext auth client for a route.
func (m *ExtAuthByRoute) AddRoute(routeID string, cfg config.ExtAuthConfig) error {
	ea, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, ea)
	return nil
}

// GetAuth returns the ext auth client for a route.
func (m *ExtAuthByRoute) GetAuth(routeID string) *ExtAuth {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns a snapshot of ext auth metrics for all routes.
func (m *ExtAuthByRoute) Stats() map[string]ExtAuthSnapshot {
	return byroute.CollectStats(&m.Manager, func(ea *ExtAuth) ExtAuthSnapshot { return ea.metrics.Snapshot() })
}

// CloseAll closes all gRPC connections.
func (m *ExtAuthByRoute) CloseAll() {
	m.Range(func(_ string, ea *ExtAuth) bool {
		ea.Close()
		return true
	})
}
