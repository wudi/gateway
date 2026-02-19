package nonce

import (
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// NonceByRoute manages per-route nonce checkers.
type NonceByRoute struct {
	byroute.Manager[*NonceChecker]
}

// NewNonceByRoute creates a new NonceByRoute manager.
func NewNonceByRoute() *NonceByRoute {
	return &NonceByRoute{}
}

// AddRoute creates and registers a nonce checker for the given route.
func (m *NonceByRoute) AddRoute(routeID string, cfg config.NonceConfig, redisClient *redis.Client) error {
	if !cfg.Enabled {
		return nil
	}

	var store NonceStore
	if cfg.Mode == "distributed" && redisClient != nil {
		store = NewRedisStore(redisClient, routeID)
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

// GetChecker returns the nonce checker for a route, or nil.
func (m *NonceByRoute) GetChecker(routeID string) *NonceChecker {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns admin status for all routes.
func (m *NonceByRoute) Stats() map[string]NonceStatus {
	return byroute.CollectStats(&m.Manager, func(nc *NonceChecker) NonceStatus { return nc.Status() })
}

// CloseAll stops all nonce checker cleanup goroutines.
func (m *NonceByRoute) CloseAll() {
	m.Range(func(_ string, nc *NonceChecker) bool {
		nc.store.Close()
		return true
	})
}

// Ensure NonceByRoute satisfies string formatting for error messages.
var _ fmt.Stringer = (*dummyStringer)(nil)

type dummyStringer struct{}

func (d *dummyStringer) String() string { return "" }
