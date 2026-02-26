package trafficshape

import (
	"context"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/config"
)

// FaultInjector injects delays and/or aborts into request processing for chaos testing.
type FaultInjector struct {
	delayPct      int
	delayDuration time.Duration
	abortPct      int
	abortStatus   int

	rng *rand.Rand
	mu  sync.Mutex

	totalRequests atomic.Int64
	totalDelayed  atomic.Int64
	totalAborted  atomic.Int64
	totalDelayNs  atomic.Int64
}

// NewFaultInjector creates a new FaultInjector from config.
func NewFaultInjector(cfg config.FaultInjectionConfig) *FaultInjector {
	return &FaultInjector{
		delayPct:      cfg.Delay.Percentage,
		delayDuration: cfg.Delay.Duration,
		abortPct:      cfg.Abort.Percentage,
		abortStatus:   cfg.Abort.StatusCode,
		rng:           rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// Apply evaluates fault injection for a request.
// Returns (aborted, statusCode). If aborted is true, the caller should write statusCode and stop.
func (fi *FaultInjector) Apply(ctx context.Context) (aborted bool, statusCode int) {
	fi.totalRequests.Add(1)

	// Roll abort first — aborted requests skip delay entirely
	if fi.abortPct > 0 && fi.roll(fi.abortPct) {
		fi.totalAborted.Add(1)
		return true, fi.abortStatus
	}

	// Roll delay
	if fi.delayPct > 0 && fi.roll(fi.delayPct) {
		start := time.Now()
		select {
		case <-time.After(fi.delayDuration):
		case <-ctx.Done():
		}
		fi.totalDelayed.Add(1)
		fi.totalDelayNs.Add(int64(time.Since(start)))
	}

	return false, 0
}

// roll returns true if a random percentage falls within the given threshold.
func (fi *FaultInjector) roll(percentage int) bool {
	if percentage >= 100 {
		return true
	}
	if percentage <= 0 {
		return false
	}
	fi.mu.Lock()
	n := fi.rng.Intn(100)
	fi.mu.Unlock()
	return n < percentage
}

// Snapshot returns a point-in-time metrics snapshot.
func (fi *FaultInjector) Snapshot() FaultInjectionSnapshot {
	return FaultInjectionSnapshot{
		TotalRequests: fi.totalRequests.Load(),
		TotalDelayed:  fi.totalDelayed.Load(),
		TotalAborted:  fi.totalAborted.Load(),
		TotalDelayNs:  fi.totalDelayNs.Load(),
	}
}

// Middleware returns a middleware that injects delays and/or aborts for chaos testing.
func (fi *FaultInjector) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			aborted, statusCode := fi.Apply(r.Context())
			if aborted {
				w.WriteHeader(statusCode)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
