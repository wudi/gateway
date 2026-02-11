package signing

import (
	"sync"

	"github.com/wudi/gateway/internal/config"
)

// SigningByRoute manages per-route request signers.
type SigningByRoute struct {
	mu      sync.RWMutex
	signers map[string]*CompiledSigner
}

// NewSigningByRoute creates a new SigningByRoute manager.
func NewSigningByRoute() *SigningByRoute {
	return &SigningByRoute{
		signers: make(map[string]*CompiledSigner),
	}
}

// AddRoute registers a signer for a route.
func (m *SigningByRoute) AddRoute(routeID string, cfg config.BackendSigningConfig) error {
	s, err := New(routeID, cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.signers[routeID] = s
	m.mu.Unlock()
	return nil
}

// GetSigner returns the signer for a route, or nil if none.
func (m *SigningByRoute) GetSigner(routeID string) *CompiledSigner {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.signers[routeID]
}

// RouteIDs returns all route IDs with signers.
func (m *SigningByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.signers))
	for id := range m.signers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns signing status for all routes.
func (m *SigningByRoute) Stats() map[string]SigningStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[string]SigningStatus, len(m.signers))
	for id, s := range m.signers {
		stats[id] = s.Status()
	}
	return stats
}
