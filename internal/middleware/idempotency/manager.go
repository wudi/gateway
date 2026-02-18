package idempotency

import (
	"sync"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/config"
)

// IdempotencyByRoute manages per-route idempotency handlers.
type IdempotencyByRoute struct {
	mu       sync.RWMutex
	handlers map[string]*CompiledIdempotency
}

// NewIdempotencyByRoute creates a new IdempotencyByRoute manager.
func NewIdempotencyByRoute() *IdempotencyByRoute {
	return &IdempotencyByRoute{}
}

// AddRoute creates and registers an idempotency handler for the given route.
func (m *IdempotencyByRoute) AddRoute(routeID string, cfg config.IdempotencyConfig, redisClient *redis.Client) error {
	if !cfg.Enabled {
		return nil
	}

	ci, err := New(routeID, cfg, redisClient)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.handlers == nil {
		m.handlers = make(map[string]*CompiledIdempotency)
	}
	m.handlers[routeID] = ci
	m.mu.Unlock()

	return nil
}

// GetHandler returns the idempotency handler for a route, or nil.
func (m *IdempotencyByRoute) GetHandler(routeID string) *CompiledIdempotency {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handlers[routeID]
}

// RouteIDs returns all route IDs with idempotency handlers.
func (m *IdempotencyByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.handlers))
	for id := range m.handlers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns admin status for all routes.
func (m *IdempotencyByRoute) Stats() map[string]IdempotencyStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]IdempotencyStatus, len(m.handlers))
	for id, ci := range m.handlers {
		result[id] = ci.Status()
	}
	return result
}

// CloseAll closes all idempotency handlers (stopping cleanup goroutines).
func (m *IdempotencyByRoute) CloseAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ci := range m.handlers {
		ci.Close()
	}
}
