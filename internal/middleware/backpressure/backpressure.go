package backpressure

import (
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/variables"
)

// Backpressure monitors backend responses and marks backends unhealthy on throttling.
type Backpressure struct {
	cfg       config.BackpressureConfig
	balancer  loadbalancer.Balancer
	statusSet map[int]bool

	timers    sync.Map // backend URL -> *time.Timer
	throttled atomic.Int64
	recovered atomic.Int64
}

// New creates a Backpressure handler.
func New(cfg config.BackpressureConfig, bal loadbalancer.Balancer) *Backpressure {
	if cfg.MaxRetryAfter <= 0 {
		cfg.MaxRetryAfter = 60 * time.Second
	}
	if cfg.DefaultDelay <= 0 {
		cfg.DefaultDelay = 5 * time.Second
	}
	codes := cfg.StatusCodes
	if len(codes) == 0 {
		codes = []int{429, 503}
	}
	statusSet := make(map[int]bool, len(codes))
	for _, c := range codes {
		statusSet[c] = true
	}
	return &Backpressure{
		cfg:       cfg,
		balancer:  bal,
		statusSet: statusSet,
	}
}

// Middleware returns the backpressure detection middleware.
func (bp *Backpressure) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusCapture{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			if !bp.statusSet[rec.code] {
				return
			}

			// Get the upstream address from variable context.
			vc := variables.GetFromRequest(r)
			addr := vc.UpstreamAddr
			if addr == "" {
				return
			}

			// Parse Retry-After header.
			delay := bp.parseRetryAfter(rec.Header().Get("Retry-After"))

			// Mark backend unhealthy.
			bp.balancer.MarkUnhealthy(addr)
			bp.throttled.Add(1)

			// Cancel existing timer for this backend.
			if old, loaded := bp.timers.LoadAndDelete(addr); loaded {
				old.(*time.Timer).Stop()
			}

			// Schedule recovery.
			timer := time.AfterFunc(delay, func() {
				bp.balancer.MarkHealthy(addr)
				bp.timers.Delete(addr)
				bp.recovered.Add(1)
			})
			bp.timers.Store(addr, timer)
		})
	}
}

func (bp *Backpressure) parseRetryAfter(val string) time.Duration {
	if val == "" {
		return bp.cfg.DefaultDelay
	}

	// Try parsing as seconds.
	if secs, err := strconv.Atoi(val); err == nil {
		d := time.Duration(secs) * time.Second
		if d > bp.cfg.MaxRetryAfter {
			return bp.cfg.MaxRetryAfter
		}
		return d
	}

	// Try parsing as HTTP-date.
	if t, err := http.ParseTime(val); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return bp.cfg.DefaultDelay
		}
		if d > bp.cfg.MaxRetryAfter {
			return bp.cfg.MaxRetryAfter
		}
		return d
	}

	return bp.cfg.DefaultDelay
}

// Close cancels all pending recovery timers.
func (bp *Backpressure) Close() {
	bp.timers.Range(func(key, value interface{}) bool {
		value.(*time.Timer).Stop()
		bp.timers.Delete(key)
		return true
	})
}

// Stats returns backpressure statistics.
func (bp *Backpressure) Stats() map[string]interface{} {
	var pending int
	bp.timers.Range(func(_, _ interface{}) bool {
		pending++
		return true
	})
	return map[string]interface{}{
		"throttled": bp.throttled.Load(),
		"recovered": bp.recovered.Load(),
		"pending":   pending,
	}
}

// statusCapture wraps ResponseWriter to capture the status code.
type statusCapture struct {
	http.ResponseWriter
	code int
}

func (sc *statusCapture) WriteHeader(code int) {
	sc.code = code
	sc.ResponseWriter.WriteHeader(code)
}

func (sc *statusCapture) Write(b []byte) (int, error) {
	if sc.code == 0 {
		sc.code = http.StatusOK
	}
	return sc.ResponseWriter.Write(b)
}

func (sc *statusCapture) Flush() {
	if f, ok := sc.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (sc *statusCapture) Unwrap() http.ResponseWriter {
	return sc.ResponseWriter
}

// BackpressureByRoute manages per-route backpressure handlers.
type BackpressureByRoute struct {
	byroute.Manager[*Backpressure]
}

// NewBackpressureByRoute creates a new per-route backpressure manager.
func NewBackpressureByRoute() *BackpressureByRoute {
	return &BackpressureByRoute{}
}

// AddRoute adds a backpressure handler for a route.
func (b *BackpressureByRoute) AddRoute(routeID string, cfg config.BackpressureConfig, bal loadbalancer.Balancer) {
	b.Add(routeID, New(cfg, bal))
}

// GetHandler returns the backpressure handler for a route.
func (b *BackpressureByRoute) GetHandler(routeID string) *Backpressure {
	v, _ := b.Get(routeID)
	return v
}

// CloseAll stops all backpressure timers.
func (b *BackpressureByRoute) CloseAll() {
	b.Range(func(_ string, bp *Backpressure) bool {
		bp.Close()
		return true
	})
}

// Stats returns per-route backpressure stats.
func (b *BackpressureByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&b.Manager, func(bp *Backpressure) interface{} {
		return bp.Stats()
	})
}
