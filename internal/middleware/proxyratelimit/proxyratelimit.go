package proxyratelimit

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/middleware"
	"golang.org/x/time/rate"
)

// ProxyLimiter rate-limits outbound requests to backends per route.
type ProxyLimiter struct {
	limiter  *rate.Limiter
	allowed  atomic.Int64
	rejected atomic.Int64
}

// New creates a ProxyLimiter from config.
func New(cfg config.ProxyRateLimitConfig) *ProxyLimiter {
	burst := cfg.Burst
	if burst == 0 {
		burst = cfg.Rate
	}
	period := cfg.Period
	if period == 0 {
		period = time.Second
	}
	rps := float64(cfg.Rate) / period.Seconds()
	return &ProxyLimiter{
		limiter: rate.NewLimiter(rate.Limit(rps), burst),
	}
}

// Middleware returns a middleware that rate-limits backend requests.
func (pl *ProxyLimiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !pl.limiter.Allow() {
				pl.rejected.Add(1)
				retryAfter := 1
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				errors.ErrServiceUnavailable.WithDetails("Backend rate limit exceeded").WriteJSON(w)
				return
			}
			pl.allowed.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns metrics for this limiter.
func (pl *ProxyLimiter) Stats() map[string]int64 {
	return map[string]int64{
		"allowed":  pl.allowed.Load(),
		"rejected": pl.rejected.Load(),
	}
}

// ProxyRateLimitByRoute manages per-route proxy rate limiters.
type ProxyRateLimitByRoute struct {
	byroute.Manager[*ProxyLimiter]
}

// NewProxyRateLimitByRoute creates a new per-route proxy rate limiter manager.
func NewProxyRateLimitByRoute() *ProxyRateLimitByRoute {
	return &ProxyRateLimitByRoute{}
}

// AddRoute adds a proxy rate limiter for a route.
func (m *ProxyRateLimitByRoute) AddRoute(routeID string, cfg config.ProxyRateLimitConfig) {
	m.Add(routeID, New(cfg))
}

// GetLimiter returns the proxy limiter for a route.
func (m *ProxyRateLimitByRoute) GetLimiter(routeID string) *ProxyLimiter {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route proxy rate limit metrics.
func (m *ProxyRateLimitByRoute) Stats() map[string]interface{} {
	stats := make(map[string]interface{})
	m.Range(func(id string, pl *ProxyLimiter) bool {
		stats[id] = pl.Stats()
		return true
	})
	return stats
}
