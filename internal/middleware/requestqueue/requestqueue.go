package requestqueue

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// RequestQueue is a bounded FIFO queue backed by a buffered channel.
// When all slots are taken, new requests block up to MaxWait, then get 503.
type RequestQueue struct {
	sem      chan struct{}
	maxWait  time.Duration
	maxDepth int

	currentDepth atomic.Int64
	enqueued     atomic.Int64
	dequeued     atomic.Int64
	rejected     atomic.Int64
	timedOut     atomic.Int64
	totalWaitNs  atomic.Int64
}

// New creates a new RequestQueue.
func New(maxDepth int, maxWait time.Duration) *RequestQueue {
	if maxDepth <= 0 {
		maxDepth = 100
	}
	if maxWait <= 0 {
		maxWait = 30 * time.Second
	}
	return &RequestQueue{
		sem:      make(chan struct{}, maxDepth),
		maxWait:  maxWait,
		maxDepth: maxDepth,
	}
}

// Middleware returns the queue middleware.
func (q *RequestQueue) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q.enqueued.Add(1)
			q.currentDepth.Add(1)
			start := time.Now()

			// Try to acquire a slot immediately.
			select {
			case q.sem <- struct{}{}:
				waited := time.Since(start)
				q.totalWaitNs.Add(int64(waited))
				defer func() {
					<-q.sem
					q.currentDepth.Add(-1)
					q.dequeued.Add(1)
				}()
				next.ServeHTTP(w, r)
				return
			default:
			}

			// Slot not immediately available — wait up to maxWait.
			ctx, cancel := context.WithTimeout(r.Context(), q.maxWait)
			defer cancel()

			select {
			case q.sem <- struct{}{}:
				waited := time.Since(start)
				q.totalWaitNs.Add(int64(waited))
				defer func() {
					<-q.sem
					q.currentDepth.Add(-1)
					q.dequeued.Add(1)
				}()
				next.ServeHTTP(w, r)
			case <-ctx.Done():
				q.currentDepth.Add(-1)
				if r.Context().Err() != nil {
					// Client cancelled, not a timeout from our queue.
					q.rejected.Add(1)
					return
				}
				q.timedOut.Add(1)
				http.Error(w, "Service Unavailable: request queue timeout", http.StatusServiceUnavailable)
			}
		})
	}
}

// QueueSnapshot is a point-in-time view of queue metrics.
type QueueSnapshot struct {
	MaxDepth     int     `json:"max_depth"`
	MaxWaitMs    float64 `json:"max_wait_ms"`
	CurrentDepth int64   `json:"current_depth"`
	Enqueued     int64   `json:"enqueued"`
	Dequeued     int64   `json:"dequeued"`
	Rejected     int64   `json:"rejected"`
	TimedOut     int64   `json:"timed_out"`
	AvgWaitMs    float64 `json:"avg_wait_ms"`
}

// Snapshot returns a point-in-time view of the queue metrics.
func (q *RequestQueue) Snapshot() QueueSnapshot {
	enqueued := q.enqueued.Load()
	totalNs := q.totalWaitNs.Load()
	var avgMs float64
	if enqueued > 0 {
		avgMs = float64(totalNs) / float64(enqueued) / 1e6
	}
	return QueueSnapshot{
		MaxDepth:     q.maxDepth,
		MaxWaitMs:    float64(q.maxWait.Milliseconds()),
		CurrentDepth: q.currentDepth.Load(),
		Enqueued:     enqueued,
		Dequeued:     q.dequeued.Load(),
		Rejected:     q.rejected.Load(),
		TimedOut:     q.timedOut.Load(),
		AvgWaitMs:    avgMs,
	}
}

// RequestQueueByRoute manages per-route request queues.
type RequestQueueByRoute struct {
	byroute.Manager[*RequestQueue]
}

// NewRequestQueueByRoute creates a new RequestQueueByRoute.
func NewRequestQueueByRoute() *RequestQueueByRoute {
	return &RequestQueueByRoute{}
}

// AddRoute creates and stores a request queue for a route.
func (m *RequestQueueByRoute) AddRoute(routeID string, cfg config.RequestQueueConfig) {
	m.Add(routeID, New(cfg.MaxDepth, cfg.MaxWait))
}

// GetQueue returns the request queue for a route, or nil.
func (m *RequestQueueByRoute) GetQueue(routeID string) *RequestQueue {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *RequestQueueByRoute) Stats() map[string]QueueSnapshot {
	return byroute.CollectStats(&m.Manager, func(q *RequestQueue) QueueSnapshot { return q.Snapshot() })
}

// MergeRequestQueueConfig merges a route-level request queue config with the global config as fallback.
func MergeRequestQueueConfig(route, global config.RequestQueueConfig) config.RequestQueueConfig {
	merged := config.MergeNonZero(global, route)
	if merged.MaxDepth == 0 {
		merged.MaxDepth = 100
	}
	if merged.MaxWait == 0 {
		merged.MaxWait = 30 * time.Second
	}
	return merged
}
