package signing

import (
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// SigningByRoute manages per-route request signers.
type SigningByRoute struct {
	byroute.Manager[*CompiledSigner]
}

// NewSigningByRoute creates a new SigningByRoute manager.
func NewSigningByRoute() *SigningByRoute {
	return &SigningByRoute{}
}

// AddRoute registers a signer for a route.
func (m *SigningByRoute) AddRoute(routeID string, cfg config.BackendSigningConfig) error {
	s, err := New(routeID, cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, s)
	return nil
}

// GetSigner returns the signer for a route, or nil if none.
func (m *SigningByRoute) GetSigner(routeID string) *CompiledSigner {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns signing status for all routes.
func (m *SigningByRoute) Stats() map[string]SigningStatus {
	return byroute.CollectStats(&m.Manager, func(s *CompiledSigner) SigningStatus { return s.Status() })
}
