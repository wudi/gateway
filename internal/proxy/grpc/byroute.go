package grpc

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// GRPCByRoute manages per-route gRPC handlers.
type GRPCByRoute struct {
	byroute.Manager[*Handler]
}

// NewGRPCByRoute creates a new per-route gRPC handler manager.
func NewGRPCByRoute() *GRPCByRoute {
	return &GRPCByRoute{}
}

// AddRoute adds a gRPC handler for a route.
func (m *GRPCByRoute) AddRoute(routeID string, cfg config.GRPCConfig) {
	m.Add(routeID, New(cfg))
}

// GetHandler returns the gRPC handler for a route.
func (m *GRPCByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route gRPC stats.
func (m *GRPCByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *Handler) interface{} {
		return h.Stats()
	})
}
