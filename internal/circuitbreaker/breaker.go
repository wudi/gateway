package circuitbreaker

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/sony/gobreaker/v2"
	"github.com/wudi/gateway/internal/config"
)

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
	State            string `json:"state"`
	FailureCount     int    `json:"failure_count"`
	SuccessCount     int    `json:"success_count"`
	FailureThreshold int    `json:"failure_threshold"`
	MaxRequests      int    `json:"max_requests"`
	TotalRequests    int64  `json:"total_requests"`
	TotalFailures    int64  `json:"total_failures"`
	TotalSuccesses   int64  `json:"total_successes"`
	TotalRejected    int64  `json:"total_rejected"`
}

// BreakerByRoute manages circuit breakers per route.
type BreakerByRoute struct {
	breakers      map[string]*Breaker
	mu            sync.RWMutex
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

// AddRoute adds a circuit breaker for a route.
func (br *BreakerByRoute) AddRoute(routeID string, cfg config.CircuitBreakerConfig) {
	br.mu.Lock()
	defer br.mu.Unlock()
	var cb func(from, to string)
	if br.onStateChange != nil {
		onSC := br.onStateChange
		rid := routeID
		cb = func(from, to string) { onSC(rid, from, to) }
	}
	if br.breakers == nil {
		br.breakers = make(map[string]*Breaker)
	}
	br.breakers[routeID] = NewBreaker(cfg, cb)
}

// GetBreaker returns the circuit breaker for a route.
func (br *BreakerByRoute) GetBreaker(routeID string) *Breaker {
	br.mu.RLock()
	defer br.mu.RUnlock()
	return br.breakers[routeID]
}

// RouteIDs returns all route IDs with circuit breakers.
func (br *BreakerByRoute) RouteIDs() []string {
	br.mu.RLock()
	defer br.mu.RUnlock()
	ids := make([]string, 0, len(br.breakers))
	for id := range br.breakers {
		ids = append(ids, id)
	}
	return ids
}

// Snapshots returns snapshots of all circuit breakers.
func (br *BreakerByRoute) Snapshots() map[string]BreakerSnapshot {
	br.mu.RLock()
	defer br.mu.RUnlock()

	result := make(map[string]BreakerSnapshot, len(br.breakers))
	for id, b := range br.breakers {
		result[id] = b.Snapshot()
	}
	return result
}
