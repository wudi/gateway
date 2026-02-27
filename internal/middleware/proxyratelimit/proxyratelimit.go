package proxyratelimit

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/middleware"
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
type ProxyRateLimitByRoute = byroute.Factory[*ProxyLimiter, config.ProxyRateLimitConfig]

// NewProxyRateLimitByRoute creates a new per-route proxy rate limiter manager.
func NewProxyRateLimitByRoute() *ProxyRateLimitByRoute {
	return byroute.SimpleFactory(New, func(pl *ProxyLimiter) any { return pl.Stats() })
}
