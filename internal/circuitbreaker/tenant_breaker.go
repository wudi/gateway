package circuitbreaker

import (
	"sync"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
)

// TenantAwareBreakerInterface extends BreakerInterface with per-tenant isolation.
type TenantAwareBreakerInterface interface {
	BreakerInterface
	// AllowForTenant checks if a request for the given tenant should be allowed.
	// If tenant isolation is enabled, each tenant gets its own circuit breaker.
	AllowForTenant(tenantID string) (func(error), error)
	// TenantSnapshots returns a snapshot of every per-tenant breaker.
	TenantSnapshots() map[string]BreakerSnapshot
}

// TenantAwareBreaker wraps a route-level circuit breaker with optional per-tenant isolation.
// When no tenant is resolved, it delegates to the route-level breaker.
type TenantAwareBreaker struct {
	routeBreaker BreakerInterface
	cfg          config.CircuitBreakerConfig
	onState      func(from, to string)
	breakers     sync.Map // tenantID -> BreakerInterface
	redisClient  *redis.Client
	routeID      string
}

// NewTenantAwareBreaker creates a tenant-aware breaker wrapping a route breaker.
func NewTenantAwareBreaker(routeBreaker BreakerInterface, cfg config.CircuitBreakerConfig, routeID string, redisClient *redis.Client, onState func(from, to string)) *TenantAwareBreaker {
	return &TenantAwareBreaker{
		routeBreaker: routeBreaker,
		cfg:          cfg,
		onState:      onState,
		redisClient:  redisClient,
		routeID:      routeID,
	}
}

// Allow delegates to the route-level breaker (used when no tenant context).
func (t *TenantAwareBreaker) Allow() (func(error), error) {
	return t.routeBreaker.Allow()
}

// Snapshot returns the route-level breaker snapshot.
func (t *TenantAwareBreaker) Snapshot() BreakerSnapshot {
	snap := t.routeBreaker.Snapshot()
	snap.TenantIsolation = true
	return snap
}

// AllowForTenant checks or creates a per-tenant breaker and delegates Allow().
func (t *TenantAwareBreaker) AllowForTenant(tenantID string) (func(error), error) {
	if tenantID == "" {
		return t.routeBreaker.Allow()
	}

	v, ok := t.breakers.Load(tenantID)
	if !ok {
		b := t.createBreaker(tenantID)
		v, _ = t.breakers.LoadOrStore(tenantID, b)
	}
	return v.(BreakerInterface).Allow()
}

// TenantSnapshots returns snapshots for all per-tenant breakers.
func (t *TenantAwareBreaker) TenantSnapshots() map[string]BreakerSnapshot {
	result := make(map[string]BreakerSnapshot)
	t.breakers.Range(func(key, value any) bool {
		result[key.(string)] = value.(BreakerInterface).Snapshot()
		return true
	})
	return result
}

func (t *TenantAwareBreaker) createBreaker(tenantID string) BreakerInterface {
	var onState func(from, to string)
	if t.onState != nil {
		onState = func(from, to string) {
			t.onState(from, to)
		}
	}

	if t.cfg.Mode == "distributed" && t.redisClient != nil {
		return NewRedisBreaker(t.routeID+":tenant:"+tenantID, t.cfg, t.redisClient, onState)
	}
	return NewBreaker(t.cfg, onState)
}
