package bluegreen

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/health"
	"github.com/wudi/gateway/internal/loadbalancer"
)

// BlueGreenByRoute manages per-route blue-green controllers.
type BlueGreenByRoute struct {
	byroute.Manager[*Controller]
}

// NewBlueGreenByRoute creates a new manager.
func NewBlueGreenByRoute() *BlueGreenByRoute {
	return &BlueGreenByRoute{}
}

// AddRoute adds a blue-green controller for a route.
func (m *BlueGreenByRoute) AddRoute(routeID string, cfg config.BlueGreenConfig, wb *loadbalancer.WeightedBalancer, hc *health.Checker) error {
	ctrl := NewController(routeID, cfg, wb, hc)
	m.Add(routeID, ctrl)
	return nil
}

// GetController returns the controller for a route, or nil if none.
func (m *BlueGreenByRoute) GetController(routeID string) *Controller {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *BlueGreenByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&m.Manager, func(c *Controller) Snapshot { return c.Snapshot() })
}

// StopAll stops all controllers.
func (m *BlueGreenByRoute) StopAll() {
	m.Range(func(_ string, c *Controller) bool {
		c.Stop()
		return true
	})
}
