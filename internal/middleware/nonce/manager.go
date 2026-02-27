package nonce

import (
	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
)

// NonceByRoute manages per-route nonce checkers.
type NonceByRoute struct {
	byroute.Manager[*NonceChecker]
	redisClient *redis.Client
}

// NewNonceByRoute creates a new NonceByRoute manager.
func NewNonceByRoute(redisClient *redis.Client) *NonceByRoute {
	return &NonceByRoute{redisClient: redisClient}
}

// AddRoute creates and registers a nonce checker for the given route.
func (m *NonceByRoute) AddRoute(routeID string, cfg config.NonceConfig) error {
	if !cfg.Enabled {
		return nil
	}

	var store NonceStore
	if cfg.Mode == "distributed" && m.redisClient != nil {
		store = NewRedisStore(m.redisClient, routeID)
	} else {
		ttl := cfg.TTL
		if ttl == 0 {
			ttl = 5 * 60e9 // 5 minutes in nanoseconds
		}
		store = NewMemoryStore(ttl)
	}

	nc := New(routeID, cfg, store)

	// Close old checker if replacing
	if old, ok := m.Get(routeID); ok {
		old.store.Close()
	}
	m.Add(routeID, nc)

	return nil
}

// Stats returns admin status for all routes.
func (m *NonceByRoute) Stats() map[string]NonceStatus {
	return byroute.CollectStats(&m.Manager, func(nc *NonceChecker) NonceStatus { return nc.Status() })
}

// CloseAll closes all nonce stores.
func (m *NonceByRoute) CloseAll() {
	byroute.ForEach(&m.Manager, func(nc *NonceChecker) { nc.CloseStore() })
}
