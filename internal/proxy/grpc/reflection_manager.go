package grpc

import (
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// ReflectionByRoute manages per-route gRPC reflection proxies.
type ReflectionByRoute struct {
	byroute.Manager[*ReflectionProxy]
}

// NewReflectionByRoute creates a new per-route gRPC reflection manager.
func NewReflectionByRoute() *ReflectionByRoute {
	return &ReflectionByRoute{}
}

// AddRoute adds a gRPC reflection proxy for a route.
func (m *ReflectionByRoute) AddRoute(routeID string, backends []string, cfg config.GRPCReflectionConfig) {
	m.Add(routeID, NewReflectionProxy(routeID, backends, cfg))
}


// Stats returns per-route gRPC reflection stats.
func (m *ReflectionByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(rp *ReflectionProxy) interface{} {
		return rp.Stats()
	})
}
