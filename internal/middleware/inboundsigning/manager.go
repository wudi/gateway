package inboundsigning

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
)

// InboundSigningByRoute manages per-route inbound signature verifiers.
type InboundSigningByRoute struct {
	byroute.Manager[*CompiledVerifier]
}

// NewInboundSigningByRoute creates a new manager.
func NewInboundSigningByRoute() *InboundSigningByRoute {
	return &InboundSigningByRoute{}
}

// AddRoute registers a verifier for a route.
func (m *InboundSigningByRoute) AddRoute(routeID string, cfg config.InboundSigningConfig) error {
	v, err := New(routeID, cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, v)
	return nil
}

// GetVerifier returns the verifier for a route, or nil if none.
func (m *InboundSigningByRoute) GetVerifier(routeID string) *CompiledVerifier {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns verifier status for all routes.
func (m *InboundSigningByRoute) Stats() map[string]VerifierStatus {
	return byroute.CollectStats(&m.Manager, func(v *CompiledVerifier) VerifierStatus { return v.Status() })
}
