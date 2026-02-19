package trafficshape

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

const (
	defaultMinConcurrency     = 5
	defaultMaxConcurrency     = 1000
	defaultLatencyTolerance   = 2.0
	defaultAdjustmentInterval = 5 * time.Second
	defaultSmoothingFactor    = 0.5
	defaultMinLatencySamples  = 25
)

// AdaptiveLimiter implements a TCP Vegas-inspired AIMD concurrency limiter.
// It dynamically adjusts the concurrency limit based on observed latency.
type AdaptiveLimiter struct {
	currentLimit atomic.Int64
	inflight     atomic.Int64

	mu              sync.Mutex
	ewmaLatency     float64 // nanoseconds
	minLatency      float64 // nanoseconds
	sampleCount     int64
	ewmaInitialized bool

	totalRequests atomic.Int64
	totalAdmitted atomic.Int64
	totalRejected atomic.Int64

	minConcurrency    int64
	maxConcurrency    int64
	latencyTolerance  float64
	smoothingFactor   float64
	minLatencySamples int

	cancel context.CancelFunc
	done   chan struct{}
}

// NewAdaptiveLimiter creates a new AdaptiveLimiter with the given config.
func NewAdaptiveLimiter(cfg config.AdaptiveConcurrencyConfig) *AdaptiveLimiter {
	minConc := int64(cfg.MinConcurrency)
	if minConc <= 0 {
		minConc = defaultMinConcurrency
	}
	maxConc := int64(cfg.MaxConcurrency)
	if maxConc <= 0 {
		maxConc = defaultMaxConcurrency
	}
	tolerance := cfg.LatencyTolerance
	if tolerance == 0 {
		tolerance = defaultLatencyTolerance
	}
	interval := cfg.AdjustmentInterval
	if interval == 0 {
		interval = defaultAdjustmentInterval
	}
	alpha := cfg.SmoothingFactor
	if alpha == 0 {
		alpha = defaultSmoothingFactor
	}
	minSamples := cfg.MinLatencySamples
	if minSamples == 0 {
		minSamples = defaultMinLatencySamples
	}

	ctx, cancel := context.WithCancel(context.Background())
	al := &AdaptiveLimiter{
		minConcurrency:    minConc,
		maxConcurrency:    maxConc,
		latencyTolerance:  tolerance,
		smoothingFactor:   alpha,
		minLatencySamples: minSamples,
		cancel:            cancel,
		done:              make(chan struct{}),
	}
	// Start at max concurrency (wide open, narrows as data arrives)
	al.currentLimit.Store(maxConc)

	go al.adjustLoop(ctx, interval)
	return al
}

// Allow attempts to acquire a concurrency slot. If the current inflight count
// is at or above the limit, it returns ok=false. On success, the caller must
// invoke the returned release function when the request completes, passing
// the HTTP status code and observed latency.
func (al *AdaptiveLimiter) Allow() (release func(statusCode int, latency time.Duration), ok bool) {
	al.totalRequests.Add(1)

	// Optimistic increment: add first, then check. This avoids the TOCTOU race
	// where multiple goroutines all read current < limit and all proceed.
	new := al.inflight.Add(1)
	if new > al.currentLimit.Load() {
		al.inflight.Add(-1)
		al.totalRejected.Add(1)
		return nil, false
	}

	al.totalAdmitted.Add(1)

	return func(statusCode int, latency time.Duration) {
		al.inflight.Add(-1)
		// Only 2xx/3xx responses contribute to latency EWMA
		if statusCode >= 200 && statusCode < 400 {
			al.recordLatency(latency)
		}
	}, true
}

// recordLatency updates the EWMA latency and min latency baseline.
func (al *AdaptiveLimiter) recordLatency(d time.Duration) {
	ns := float64(d.Nanoseconds())
	al.mu.Lock()
	defer al.mu.Unlock()

	al.sampleCount++

	if !al.ewmaInitialized {
		al.ewmaLatency = ns
		al.minLatency = ns
		al.ewmaInitialized = true
		return
	}

	// EWMA update
	al.ewmaLatency = al.smoothingFactor*ns + (1-al.smoothingFactor)*al.ewmaLatency

	// Slowly decay minLatency toward EWMA (1% per sample)
	if ns < al.minLatency {
		al.minLatency = ns
	} else {
		al.minLatency = al.minLatency + 0.01*(al.ewmaLatency-al.minLatency)
	}
}

// adjust performs one AIMD adjustment cycle.
func (al *AdaptiveLimiter) adjust() {
	al.mu.Lock()
	samples := al.sampleCount
	ewma := al.ewmaLatency
	minLat := al.minLatency
	al.mu.Unlock()

	if samples < int64(al.minLatencySamples) {
		return
	}
	if minLat <= 0 {
		return
	}

	gradient := ewma / minLat
	limit := al.currentLimit.Load()

	if gradient < al.latencyTolerance {
		// Additive increase
		limit++
	} else {
		// Multiplicative decrease
		newLimit := int64(math.Floor(float64(limit) * minLat / ewma))
		limit = newLimit
	}

	// Clamp
	if limit < al.minConcurrency {
		limit = al.minConcurrency
	}
	if limit > al.maxConcurrency {
		limit = al.maxConcurrency
	}

	al.currentLimit.Store(limit)
}

// adjustLoop runs the periodic adjustment in a background goroutine.
func (al *AdaptiveLimiter) adjustLoop(ctx context.Context, interval time.Duration) {
	defer close(al.done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			al.adjust()
		}
	}
}

// Stop halts the background adjustment goroutine and waits for it to finish.
func (al *AdaptiveLimiter) Stop() {
	al.cancel()
	<-al.done
}

// Snapshot returns a point-in-time snapshot of the limiter's state.
func (al *AdaptiveLimiter) Snapshot() AdaptiveConcurrencySnapshot {
	al.mu.Lock()
	ewma := al.ewmaLatency
	minLat := al.minLatency
	samples := al.sampleCount
	al.mu.Unlock()

	return AdaptiveConcurrencySnapshot{
		CurrentLimit:  al.currentLimit.Load(),
		InFlight:      al.inflight.Load(),
		EWMALatencyMs: ewma / float64(time.Millisecond),
		MinLatencyMs:  minLat / float64(time.Millisecond),
		Samples:       samples,
		TotalRequests: al.totalRequests.Load(),
		TotalAdmitted: al.totalAdmitted.Load(),
		TotalRejected: al.totalRejected.Load(),
	}
}

// AdaptiveConcurrencyByRoute manages per-route adaptive concurrency limiters.
type AdaptiveConcurrencyByRoute struct {
	byroute.Manager[*AdaptiveLimiter]
}

// NewAdaptiveConcurrencyByRoute creates a new manager.
func NewAdaptiveConcurrencyByRoute() *AdaptiveConcurrencyByRoute {
	return &AdaptiveConcurrencyByRoute{}
}

// AddRoute creates and stores an adaptive limiter for a route.
func (m *AdaptiveConcurrencyByRoute) AddRoute(routeID string, cfg config.AdaptiveConcurrencyConfig) {
	m.Add(routeID, NewAdaptiveLimiter(cfg))
}

// GetLimiter returns the adaptive limiter for a route, or nil.
func (m *AdaptiveConcurrencyByRoute) GetLimiter(routeID string) *AdaptiveLimiter {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *AdaptiveConcurrencyByRoute) Stats() map[string]AdaptiveConcurrencySnapshot {
	return byroute.CollectStats(&m.Manager, func(l *AdaptiveLimiter) AdaptiveConcurrencySnapshot { return l.Snapshot() })
}

// StopAll stops all adaptive limiters.
func (m *AdaptiveConcurrencyByRoute) StopAll() {
	m.Range(func(_ string, l *AdaptiveLimiter) bool {
		l.Stop()
		return true
	})
}

// MergeAdaptiveConcurrencyConfig merges a route-level config with the global config as fallback.
func MergeAdaptiveConcurrencyConfig(route, global config.AdaptiveConcurrencyConfig) config.AdaptiveConcurrencyConfig {
	return config.MergeNonZero(global, route)
}
