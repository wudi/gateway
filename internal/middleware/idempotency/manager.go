package idempotency

import (
	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// IdempotencyByRoute manages per-route idempotency handlers.
type IdempotencyByRoute struct {
	byroute.Manager[*CompiledIdempotency]
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

	m.Add(routeID, ci)

	return nil
}

// GetHandler returns the idempotency handler for a route, or nil.
func (m *IdempotencyByRoute) GetHandler(routeID string) *CompiledIdempotency {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns admin status for all routes.
func (m *IdempotencyByRoute) Stats() map[string]IdempotencyStatus {
	return byroute.CollectStats(&m.Manager, func(ci *CompiledIdempotency) IdempotencyStatus { return ci.Status() })
}

// CloseAll closes all idempotency handlers (stopping cleanup goroutines).
func (m *IdempotencyByRoute) CloseAll() {
	m.Range(func(_ string, ci *CompiledIdempotency) bool {
		ci.Close()
		return true
	})
}
