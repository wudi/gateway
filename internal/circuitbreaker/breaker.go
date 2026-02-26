package circuitbreaker

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sony/gobreaker/v2"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// BreakerInterface is implemented by both local and distributed circuit breakers.
type BreakerInterface interface {
	Allow() (func(error), error)
	Snapshot() BreakerSnapshot
}

// Breaker wraps gobreaker.TwoStepCircuitBreaker with lifetime counters.
type Breaker struct {
	cb *gobreaker.TwoStepCircuitBreaker[any]

	// Lifetime atomic counters (not reset by gobreaker)
	totalRequests  atomic.Int64
	totalSuccesses atomic.Int64
	totalFailures  atomic.Int64
	totalRejected  atomic.Int64

	failureThreshold int
	maxRequests      uint32
}

// NewBreaker creates a new circuit breaker backed by gobreaker v2.
// onStateChange is called when the breaker transitions between states (may be nil).
func NewBreaker(cfg config.CircuitBreakerConfig, onStateChange func(from, to string)) *Breaker {
	failureThreshold := cfg.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = 5
	}

	maxRequests := cfg.MaxRequests
	if maxRequests <= 0 {
		maxRequests = 1
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	b := &Breaker{
		failureThreshold: failureThreshold,
		maxRequests:      uint32(maxRequests),
	}

	settings := gobreaker.Settings{
		MaxRequests: uint32(maxRequests),
		Timeout:     timeout,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return int(counts.ConsecutiveFailures) >= failureThreshold
		},
	}
	if onStateChange != nil {
		settings.OnStateChange = func(name string, from gobreaker.State, to gobreaker.State) {
			onStateChange(stateString(from), stateString(to))
		}
	}
	b.cb = gobreaker.NewTwoStepCircuitBreaker[any](settings)

	return b
}

// Allow checks if a request should be allowed. On success it returns a
// callback that the caller MUST invoke with nil (success) or non-nil (failure).
func (b *Breaker) Allow() (func(error), error) {
	b.totalRequests.Add(1)

	done, err := b.cb.Allow()
	if err != nil {
		b.totalRejected.Add(1)
		return nil, err
	}

	// Wrap the done callback to update our lifetime counters.
	return func(err error) {
		done(err)
		if err == nil {
			b.totalSuccesses.Add(1)
		} else {
			b.totalFailures.Add(1)
		}
	}, nil
}

// stateString converts gobreaker.State to our string representation.
func stateString(s gobreaker.State) string {
	switch s {
	case gobreaker.StateClosed:
		return "closed"
	case gobreaker.StateOpen:
		return "open"
	case gobreaker.StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Snapshot returns a point-in-time view of the breaker state.
func (b *Breaker) Snapshot() BreakerSnapshot {
	counts := b.cb.Counts()
	return BreakerSnapshot{
		State:            stateString(b.cb.State()),
		Mode:             "local",
		FailureCount:     int(counts.ConsecutiveFailures),
		SuccessCount:     int(counts.ConsecutiveSuccesses),
		FailureThreshold: b.failureThreshold,
		MaxRequests:      int(b.maxRequests),
		TotalRequests:    b.totalRequests.Load(),
		TotalFailures:    b.totalFailures.Load(),
		TotalSuccesses:   b.totalSuccesses.Load(),
		TotalRejected:    b.totalRejected.Load(),
	}
}

// BreakerSnapshot is a point-in-time view of a circuit breaker.
type BreakerSnapshot struct {
	State            string                    `json:"state"`
	Mode             string                    `json:"mode"`
	Override         string                    `json:"override,omitempty"`
	FailureCount     int                       `json:"failure_count"`
	SuccessCount     int                       `json:"success_count"`
	FailureThreshold int                       `json:"failure_threshold"`
	MaxRequests      int                       `json:"max_requests"`
	TotalRequests    int64                     `json:"total_requests"`
	TotalFailures    int64                     `json:"total_failures"`
	TotalSuccesses   int64                     `json:"total_successes"`
	TotalRejected    int64                     `json:"total_rejected"`
	TenantIsolation  bool                      `json:"tenant_isolation,omitempty"`
	TenantBreakers   map[string]BreakerSnapshot `json:"tenant_breakers,omitempty"`
}

// BreakerByRoute manages circuit breakers per route.
type BreakerByRoute struct {
	byroute.Manager[BreakerInterface]
	mu            sync.Mutex
	onStateChange func(routeID, from, to string)
}

// NewBreakerByRoute creates a new route-based circuit breaker manager.
func NewBreakerByRoute() *BreakerByRoute {
	return &BreakerByRoute{}
}

// SetOnStateChange registers a callback invoked when any breaker changes state.
func (br *BreakerByRoute) SetOnStateChange(cb func(routeID, from, to string)) {
	br.mu.Lock()
	defer br.mu.Unlock()
	br.onStateChange = cb
}

// AddRoute adds a local circuit breaker for a route.
// If cfg.TenantIsolation is true, the breaker is wrapped with a TenantAwareBreaker.
// All breakers are wrapped in an OverridableBreaker for admin control.
func (br *BreakerByRoute) AddRoute(routeID string, cfg config.CircuitBreakerConfig) {
	br.mu.Lock()
	var cb func(from, to string)
	if br.onStateChange != nil {
		onSC := br.onStateChange
		rid := routeID
		cb = func(from, to string) { onSC(rid, from, to) }
	}
	br.mu.Unlock()
	b := NewBreaker(cfg, cb)
	if cfg.TenantIsolation {
		br.Add(routeID, &OverridableBreaker{inner: NewTenantAwareBreaker(b, cfg, routeID, nil, cb)})
	} else {
		br.Add(routeID, &OverridableBreaker{inner: b})
	}
}

// AddRouteDistributed adds a Redis-backed distributed circuit breaker for a route.
// If cfg.TenantIsolation is true, the breaker is wrapped with a TenantAwareBreaker.
// All breakers are wrapped in an OverridableBreaker for admin control.
func (br *BreakerByRoute) AddRouteDistributed(routeID string, cfg config.CircuitBreakerConfig, client *redis.Client) {
	br.mu.Lock()
	var cb func(from, to string)
	if br.onStateChange != nil {
		onSC := br.onStateChange
		rid := routeID
		cb = func(from, to string) { onSC(rid, from, to) }
	}
	br.mu.Unlock()
	b := NewRedisBreaker(routeID, cfg, client, cb)
	if cfg.TenantIsolation {
		br.Add(routeID, &OverridableBreaker{inner: NewTenantAwareBreaker(b, cfg, routeID, client, cb)})
	} else {
		br.Add(routeID, &OverridableBreaker{inner: b})
	}
}

// GetBreaker returns the circuit breaker for a route.
func (br *BreakerByRoute) GetBreaker(routeID string) BreakerInterface {
	v, _ := br.Get(routeID)
	return v
}

// Snapshots returns snapshots of all circuit breakers.
// For tenant-aware breakers, tenant sub-snapshots are included.
func (br *BreakerByRoute) Snapshots() map[string]BreakerSnapshot {
	result := make(map[string]BreakerSnapshot)
	br.Range(func(id string, b BreakerInterface) bool {
		snap := b.Snapshot()
		if tab, ok := b.(TenantAwareBreakerInterface); ok {
			snap.TenantBreakers = tab.TenantSnapshots()
		}
		result[id] = snap
		return true
	})
	return result
}

// ForceOpen forces the circuit breaker for a route into the open state,
// rejecting all requests regardless of the underlying breaker state.
func (br *BreakerByRoute) ForceOpen(routeID string) error {
	b, ok := br.Get(routeID)
	if !ok {
		return fmt.Errorf("no circuit breaker for route %q", routeID)
	}
	ob, ok := b.(*OverridableBreaker)
	if !ok {
		return fmt.Errorf("circuit breaker for route %q is not overridable", routeID)
	}
	ob.override.Store(overrideForceOpen)
	return nil
}

// ForceClose forces the circuit breaker for a route into the closed state,
// allowing all requests regardless of the underlying breaker state.
func (br *BreakerByRoute) ForceClose(routeID string) error {
	b, ok := br.Get(routeID)
	if !ok {
		return fmt.Errorf("no circuit breaker for route %q", routeID)
	}
	ob, ok := b.(*OverridableBreaker)
	if !ok {
		return fmt.Errorf("circuit breaker for route %q is not overridable", routeID)
	}
	ob.override.Store(overrideForceClosed)
	return nil
}

// ResetOverride removes any admin override, returning the circuit breaker
// to automatic state management.
func (br *BreakerByRoute) ResetOverride(routeID string) error {
	b, ok := br.Get(routeID)
	if !ok {
		return fmt.Errorf("no circuit breaker for route %q", routeID)
	}
	ob, ok := b.(*OverridableBreaker)
	if !ok {
		return fmt.Errorf("circuit breaker for route %q is not overridable", routeID)
	}
	ob.override.Store(overrideAuto)
	return nil
}

// OverridableBreaker wraps a BreakerInterface with admin override support.
type OverridableBreaker struct {
	inner    BreakerInterface
	override atomic.Int32 // 0=auto, 1=forced_open, 2=forced_closed
}

const (
	overrideAuto        = 0
	overrideForceOpen   = 1
	overrideForceClosed = 2
)

// Allow checks if a request should be allowed, respecting admin overrides.
func (ob *OverridableBreaker) Allow() (func(error), error) {
	switch ob.override.Load() {
	case overrideForceOpen:
		return nil, gobreaker.ErrOpenState
	case overrideForceClosed:
		// Bypass inner breaker but still record outcomes through it.
		done, err := ob.inner.Allow()
		if err != nil {
			// Inner breaker rejected, but override says allow.
			// Return a no-op callback.
			return func(error) {}, nil
		}
		return done, nil
	default:
		return ob.inner.Allow()
	}
}

// Snapshot returns a point-in-time view including the override state.
func (ob *OverridableBreaker) Snapshot() BreakerSnapshot {
	snap := ob.inner.Snapshot()
	switch ob.override.Load() {
	case overrideForceOpen:
		snap.Override = "forced_open"
	case overrideForceClosed:
		snap.Override = "forced_closed"
	default:
		snap.Override = "none"
	}
	return snap
}

// AllowForTenant delegates to the inner breaker's tenant-aware Allow if available,
// respecting admin overrides. This allows OverridableBreaker to implement
// TenantAwareBreakerInterface when the inner breaker supports it.
func (ob *OverridableBreaker) AllowForTenant(tenantID string) (func(error), error) {
	switch ob.override.Load() {
	case overrideForceOpen:
		return nil, gobreaker.ErrOpenState
	case overrideForceClosed:
		tab, ok := ob.inner.(TenantAwareBreakerInterface)
		if !ok {
			return func(error) {}, nil
		}
		done, err := tab.AllowForTenant(tenantID)
		if err != nil {
			return func(error) {}, nil
		}
		return done, nil
	default:
		tab, ok := ob.inner.(TenantAwareBreakerInterface)
		if !ok {
			return ob.inner.Allow()
		}
		return tab.AllowForTenant(tenantID)
	}
}

// TenantSnapshots delegates to the inner breaker if it supports tenant isolation.
func (ob *OverridableBreaker) TenantSnapshots() map[string]BreakerSnapshot {
	if tab, ok := ob.inner.(TenantAwareBreakerInterface); ok {
		return tab.TenantSnapshots()
	}
	return nil
}
