package circuitbreaker

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/gateway/internal/config"
)

// State represents the circuit breaker state
type State int

const (
	StateClosed   State = iota // Normal operation
	StateOpen                  // Failing, reject requests
	StateHalfOpen              // Testing recovery
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Breaker implements the circuit breaker pattern
type Breaker struct {
	mu               sync.Mutex
	state            State
	failureCount     int
	successCount     int
	halfOpenCount    int
	failureThreshold int
	successThreshold int
	halfOpenRequests int
	timeout          time.Duration
	lastFailureTime  time.Time

	// Metrics (atomic for lock-free reads)
	totalRequests  atomic.Int64
	totalFailures  atomic.Int64
	totalSuccesses atomic.Int64
	totalRejected  atomic.Int64
}

// NewBreaker creates a new circuit breaker
func NewBreaker(cfg config.CircuitBreakerConfig) *Breaker {
	failureThreshold := cfg.FailureThreshold
	if failureThreshold <= 0 {
		failureThreshold = 5
	}

	successThreshold := cfg.SuccessThreshold
	if successThreshold <= 0 {
		successThreshold = 2
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	halfOpenRequests := cfg.HalfOpenRequests
	if halfOpenRequests <= 0 {
		halfOpenRequests = 1
	}

	return &Breaker{
		state:            StateClosed,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
		halfOpenRequests: halfOpenRequests,
		timeout:          timeout,
	}
}

// Allow checks if a request should be allowed through the circuit breaker
func (b *Breaker) Allow() (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.totalRequests.Add(1)

	switch b.state {
	case StateClosed:
		return true, nil

	case StateOpen:
		// Check if timeout has elapsed
		if time.Since(b.lastFailureTime) >= b.timeout {
			b.state = StateHalfOpen
			b.halfOpenCount = 1 // This request counts as the first half-open request
			b.successCount = 0
			b.failureCount = 0
			return true, nil
		}
		b.totalRejected.Add(1)
		return false, fmt.Errorf("circuit breaker is open")

	case StateHalfOpen:
		if b.halfOpenCount < b.halfOpenRequests {
			b.halfOpenCount++
			return true, nil
		}
		b.totalRejected.Add(1)
		return false, fmt.Errorf("circuit breaker is half-open, max concurrent requests reached")
	}

	return false, fmt.Errorf("unknown circuit breaker state")
}

// RecordSuccess records a successful request
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.totalSuccesses.Add(1)

	switch b.state {
	case StateClosed:
		b.failureCount = 0

	case StateHalfOpen:
		b.successCount++
		if b.successCount >= b.successThreshold {
			b.state = StateClosed
			b.failureCount = 0
			b.successCount = 0
			b.halfOpenCount = 0
		}
	}
}

// RecordFailure records a failed request
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.totalFailures.Add(1)

	switch b.state {
	case StateClosed:
		b.failureCount++
		if b.failureCount >= b.failureThreshold {
			b.state = StateOpen
			b.lastFailureTime = time.Now()
		}

	case StateHalfOpen:
		b.state = StateOpen
		b.lastFailureTime = time.Now()
		b.halfOpenCount = 0
		b.successCount = 0
	}
}

// Snapshot returns a point-in-time view of the breaker state
func (b *Breaker) Snapshot() BreakerSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()

	return BreakerSnapshot{
		State:            b.state.String(),
		FailureCount:     b.failureCount,
		SuccessCount:     b.successCount,
		FailureThreshold: b.failureThreshold,
		SuccessThreshold: b.successThreshold,
		TotalRequests:    b.totalRequests.Load(),
		TotalFailures:    b.totalFailures.Load(),
		TotalSuccesses:   b.totalSuccesses.Load(),
		TotalRejected:    b.totalRejected.Load(),
	}
}

// BreakerSnapshot is a point-in-time view of a circuit breaker
type BreakerSnapshot struct {
	State            string `json:"state"`
	FailureCount     int    `json:"failure_count"`
	SuccessCount     int    `json:"success_count"`
	FailureThreshold int    `json:"failure_threshold"`
	SuccessThreshold int    `json:"success_threshold"`
	TotalRequests    int64  `json:"total_requests"`
	TotalFailures    int64  `json:"total_failures"`
	TotalSuccesses   int64  `json:"total_successes"`
	TotalRejected    int64  `json:"total_rejected"`
}

// BreakerByRoute manages circuit breakers per route
type BreakerByRoute struct {
	breakers map[string]*Breaker
	mu       sync.RWMutex
}

// NewBreakerByRoute creates a new route-based circuit breaker manager
func NewBreakerByRoute() *BreakerByRoute {
	return &BreakerByRoute{
		breakers: make(map[string]*Breaker),
	}
}

// AddRoute adds a circuit breaker for a route
func (br *BreakerByRoute) AddRoute(routeID string, cfg config.CircuitBreakerConfig) {
	br.mu.Lock()
	defer br.mu.Unlock()
	br.breakers[routeID] = NewBreaker(cfg)
}

// GetBreaker returns the circuit breaker for a route
func (br *BreakerByRoute) GetBreaker(routeID string) *Breaker {
	br.mu.RLock()
	defer br.mu.RUnlock()
	return br.breakers[routeID]
}

// Snapshots returns snapshots of all circuit breakers
func (br *BreakerByRoute) Snapshots() map[string]BreakerSnapshot {
	br.mu.RLock()
	defer br.mu.RUnlock()

	result := make(map[string]BreakerSnapshot, len(br.breakers))
	for id, b := range br.breakers {
		result[id] = b.Snapshot()
	}
	return result
}
