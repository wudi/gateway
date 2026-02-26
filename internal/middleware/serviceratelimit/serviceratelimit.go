package serviceratelimit

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/middleware"
	"golang.org/x/time/rate"
)

// ServiceLimiter enforces a global runway-wide throughput cap.
type ServiceLimiter struct {
	limiter  *rate.Limiter
	allowed  atomic.Int64
	rejected atomic.Int64
}

// New creates a ServiceLimiter from config.
func New(cfg config.ServiceRateLimitConfig) *ServiceLimiter {
	burst := cfg.Burst
	if burst == 0 {
		burst = cfg.Rate
	}
	period := cfg.Period
	if period == 0 {
		period = time.Second
	}
	rps := float64(cfg.Rate) / period.Seconds()
	return &ServiceLimiter{
		limiter: rate.NewLimiter(rate.Limit(rps), burst),
	}
}

// Middleware returns a middleware that enforces the service-level rate limit.
func (sl *ServiceLimiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !sl.limiter.Allow() {
				sl.rejected.Add(1)
				w.Header().Set("Retry-After", strconv.Itoa(1))
				errors.New(http.StatusTooManyRequests, "Service rate limit exceeded").WriteJSON(w)
				return
			}
			sl.allowed.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns metrics for this limiter.
func (sl *ServiceLimiter) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":  true,
		"allowed":  sl.allowed.Load(),
		"rejected": sl.rejected.Load(),
	}
}
