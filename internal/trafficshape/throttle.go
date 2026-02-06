package trafficshape

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/example/gateway/internal/variables"
)

// Throttler delays requests using a token bucket limiter.
type Throttler struct {
	limiter *rate.Limiter // used when PerIP is false
	maxWait time.Duration
	perIP   bool

	// Per-IP limiters
	ipLimiters map[string]*rate.Limiter
	ipMu       sync.Mutex
	ipRate     rate.Limit
	ipBurst    int

	// Metrics
	totalRequests  atomic.Int64
	totalThrottled atomic.Int64
	totalTimedOut  atomic.Int64
	totalWaitNs    atomic.Int64
}

// NewThrottler creates a new Throttler.
func NewThrottler(rps int, burst int, maxWait time.Duration, perIP bool) *Throttler {
	if burst <= 0 {
		burst = rps
	}
	if maxWait <= 0 {
		maxWait = 30 * time.Second
	}
	t := &Throttler{
		maxWait:    maxWait,
		perIP:      perIP,
		ipRate:     rate.Limit(rps),
		ipBurst:    burst,
		ipLimiters: make(map[string]*rate.Limiter),
	}
	if !perIP {
		t.limiter = rate.NewLimiter(rate.Limit(rps), burst)
	} else {
		go t.cleanupLoop()
	}
	return t
}

// Throttle blocks until a token is available or the deadline expires.
// Returns nil on success, context.DeadlineExceeded on timeout.
func (t *Throttler) Throttle(ctx context.Context, r *http.Request) error {
	t.totalRequests.Add(1)

	lim := t.getLimiter(r)

	// Derive a context with the maxWait deadline
	deadline, ok := ctx.Deadline()
	maxDeadline := time.Now().Add(t.maxWait)
	if !ok || maxDeadline.Before(deadline) {
		deadline = maxDeadline
	}
	waitCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	start := time.Now()
	err := lim.Wait(waitCtx)
	waited := time.Since(start)

	if err != nil {
		t.totalTimedOut.Add(1)
		return err
	}

	if waited > time.Millisecond {
		t.totalThrottled.Add(1)
		t.totalWaitNs.Add(int64(waited))
	}
	return nil
}

func (t *Throttler) getLimiter(r *http.Request) *rate.Limiter {
	if !t.perIP {
		return t.limiter
	}
	ip := variables.ExtractClientIP(r)
	t.ipMu.Lock()
	defer t.ipMu.Unlock()
	lim, ok := t.ipLimiters[ip]
	if !ok {
		lim = rate.NewLimiter(t.ipRate, t.ipBurst)
		t.ipLimiters[ip] = lim
	}
	return lim
}

func (t *Throttler) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		t.ipMu.Lock()
		// Remove limiters that have full tokens (idle)
		for ip, lim := range t.ipLimiters {
			if lim.Tokens() >= float64(t.ipBurst) {
				delete(t.ipLimiters, ip)
			}
		}
		t.ipMu.Unlock()
	}
}

// Snapshot returns a point-in-time metrics snapshot.
func (t *Throttler) Snapshot() ThrottleSnapshot {
	total := t.totalRequests.Load()
	throttled := t.totalThrottled.Load()
	timedOut := t.totalTimedOut.Load()
	waitNs := t.totalWaitNs.Load()

	var avgWaitMs float64
	if throttled > 0 {
		avgWaitMs = float64(waitNs) / float64(throttled) / 1e6
	}

	return ThrottleSnapshot{
		TotalRequests:  total,
		TotalThrottled: throttled,
		TotalTimedOut:  timedOut,
		AvgWaitMs:      avgWaitMs,
	}
}
