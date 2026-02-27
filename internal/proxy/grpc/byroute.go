package grpc

import (
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// GRPCByRoute manages per-route gRPC handlers.
type GRPCByRoute = byroute.Factory[*Handler, config.GRPCConfig]

// NewGRPCByRoute creates a new per-route gRPC handler manager.
func NewGRPCByRoute() *GRPCByRoute {
	return byroute.SimpleFactory(New, func(h *Handler) any {
		return h.Stats()
	})
}
