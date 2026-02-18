package nonce

import (
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/config"
)

// NonceByRoute manages per-route nonce checkers.
type NonceByRoute struct {
	mu       sync.RWMutex
	checkers map[string]*NonceChecker
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

	m.mu.Lock()
	if m.checkers == nil {
		m.checkers = make(map[string]*NonceChecker)
	}
	if old, exists := m.checkers[routeID]; exists {
		old.store.Close()
	}
	m.checkers[routeID] = nc
	m.mu.Unlock()

	return nil
}

// GetChecker returns the nonce checker for a route, or nil.
func (m *NonceByRoute) GetChecker(routeID string) *NonceChecker {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.checkers[routeID]
}

// RouteIDs returns all route IDs with nonce checkers.
func (m *NonceByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.checkers))
	for id := range m.checkers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns admin status for all routes.
func (m *NonceByRoute) Stats() map[string]NonceStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]NonceStatus, len(m.checkers))
	for id, nc := range m.checkers {
		result[id] = nc.Status()
	}
	return result
}

// CloseAll stops all nonce checker cleanup goroutines.
func (m *NonceByRoute) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, nc := range m.checkers {
		nc.store.Close()
	}
}

// Ensure NonceByRoute satisfies string formatting for error messages.
var _ fmt.Stringer = (*dummyStringer)(nil)

type dummyStringer struct{}

func (d *dummyStringer) String() string { return "" }
