package extauth

import (
	"sync"

	"github.com/example/gateway/internal/config"
)

// ExtAuthByRoute manages per-route external auth clients.
type ExtAuthByRoute struct {
	auths map[string]*ExtAuth
	mu    sync.RWMutex
}

// NewExtAuthByRoute creates a new per-route ext auth manager.
func NewExtAuthByRoute() *ExtAuthByRoute {
	return &ExtAuthByRoute{
		auths: make(map[string]*ExtAuth),
	}
}

// AddRoute adds an ext auth client for a route.
func (m *ExtAuthByRoute) AddRoute(routeID string, cfg config.ExtAuthConfig) error {
	ea, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.auths[routeID] = ea
	m.mu.Unlock()
	return nil
}

// GetAuth returns the ext auth client for a route.
func (m *ExtAuthByRoute) GetAuth(routeID string) *ExtAuth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.auths[routeID]
}

// RouteIDs returns the list of route IDs with ext auth configured.
func (m *ExtAuthByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.auths))
	for id := range m.auths {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns a snapshot of ext auth metrics for all routes.
func (m *ExtAuthByRoute) Stats() map[string]ExtAuthSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]ExtAuthSnapshot, len(m.auths))
	for id, ea := range m.auths {
		result[id] = ea.metrics.Snapshot()
	}
	return result
}

// CloseAll closes all gRPC connections.
func (m *ExtAuthByRoute) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ea := range m.auths {
		ea.Close()
	}
}
