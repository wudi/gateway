package responselimit

import (
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// ResponseLimiter enforces a maximum response body size for a route.
type ResponseLimiter struct {
	maxSize int64
	action  string // "reject" (default), "truncate", "log_only"
	metrics Metrics
}

// Metrics tracks response limit metrics.
type Metrics struct {
	TotalResponses atomic.Int64
	Limited        atomic.Int64
	TotalBytes     atomic.Int64
}

// Snapshot is the JSON-serializable metrics form.
type Snapshot struct {
	TotalResponses int64  `json:"total_responses"`
	Limited        int64  `json:"limited"`
	TotalBytes     int64  `json:"total_bytes"`
	MaxSize        int64  `json:"max_size"`
	Action         string `json:"action"`
}

// New creates a ResponseLimiter from config.
func New(cfg config.ResponseLimitConfig) *ResponseLimiter {
	action := cfg.Action
	if action == "" {
		action = "reject"
	}
	return &ResponseLimiter{
		maxSize: cfg.MaxSize,
		action:  action,
	}
}

// IsEnabled returns true if the limiter has a positive max size.
func (rl *ResponseLimiter) IsEnabled() bool {
	return rl.maxSize > 0
}

// Stats returns a snapshot of the limiter's metrics.
func (rl *ResponseLimiter) Stats() Snapshot {
	return Snapshot{
		TotalResponses: rl.metrics.TotalResponses.Load(),
		Limited:        rl.metrics.Limited.Load(),
		TotalBytes:     rl.metrics.TotalBytes.Load(),
		MaxSize:        rl.maxSize,
		Action:         rl.action,
	}
}

// Middleware returns the response size limiting middleware.
func (rl *ResponseLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rl.metrics.TotalResponses.Add(1)

			lw := &limitWriter{
				ResponseWriter: w,
				limiter:        rl,
				remaining:      rl.maxSize,
			}
			next.ServeHTTP(lw, r)
			rl.metrics.TotalBytes.Add(lw.written)
		})
	}
}

// limitWriter wraps http.ResponseWriter to track and enforce response body size.
type limitWriter struct {
	http.ResponseWriter
	limiter    *ResponseLimiter
	written    int64
	remaining  int64
	limited    bool
	headerSent bool
}

// WriteHeader intercepts the status code.
// For "reject" mode: if Content-Length is known and exceeds max, replace with 502.
func (lw *limitWriter) WriteHeader(statusCode int) {
	if lw.headerSent {
		return
	}

	if lw.limiter.action == "reject" {
		if cl := lw.Header().Get("Content-Length"); cl != "" {
			if size, err := strconv.ParseInt(cl, 10, 64); err == nil && size > lw.limiter.maxSize {
				lw.limited = true
				lw.limiter.metrics.Limited.Add(1)
				lw.headerSent = true
				lw.Header().Del("Content-Length")
				lw.Header().Set("X-Response-Limited", "true")
				lw.ResponseWriter.WriteHeader(http.StatusBadGateway)
				return
			}
		}
	}

	lw.headerSent = true
	lw.ResponseWriter.WriteHeader(statusCode)
}

// Write enforces the size limit on response body writes.
func (lw *limitWriter) Write(p []byte) (int, error) {
	if !lw.headerSent {
		lw.WriteHeader(http.StatusOK)
	}

	if lw.limited {
		// Already rejected — discard further writes
		return len(p), nil
	}

	n := int64(len(p))

	switch lw.limiter.action {
	case "truncate":
		if lw.remaining <= 0 {
			return len(p), nil // discard, pretend success
		}
		if n > lw.remaining {
			// Only write what remains
			if !lw.limited {
				lw.limited = true
				lw.limiter.metrics.Limited.Add(1)
			}
			written, err := lw.ResponseWriter.Write(p[:lw.remaining])
			lw.written += int64(written)
			lw.remaining = 0
			return len(p), err // report full len to prevent proxy errors
		}
		written, err := lw.ResponseWriter.Write(p)
		lw.written += int64(written)
		lw.remaining -= int64(written)
		return written, err

	case "log_only":
		written, err := lw.ResponseWriter.Write(p)
		lw.written += int64(written)
		if lw.written > lw.limiter.maxSize && !lw.limited {
			lw.limited = true
			lw.limiter.metrics.Limited.Add(1)
		}
		return written, err

	default: // "reject"
		if lw.written+n > lw.limiter.maxSize {
			if !lw.limited {
				lw.limited = true
				lw.limiter.metrics.Limited.Add(1)
				// For streaming responses where Content-Length wasn't set,
				// we can't change the status code anymore. Stop writing.
				lw.Header().Set("X-Response-Limited", "true")
			}
			return len(p), nil // discard
		}
		written, err := lw.ResponseWriter.Write(p)
		lw.written += int64(written)
		lw.remaining -= int64(written)
		return written, err
	}
}

// Flush implements http.Flusher.
func (lw *limitWriter) Flush() {
	if f, ok := lw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// MergeResponseLimitConfig merges per-route config with global config.
// Per-route non-zero values override global.
func MergeResponseLimitConfig(perRoute, global config.ResponseLimitConfig) config.ResponseLimitConfig {
	merged := global
	if perRoute.Enabled {
		merged.Enabled = true
	}
	if perRoute.MaxSize > 0 {
		merged.MaxSize = perRoute.MaxSize
	}
	if perRoute.Action != "" {
		merged.Action = perRoute.Action
	}
	return merged
}

// ResponseLimitByRoute manages response limiters per route.
type ResponseLimitByRoute struct {
	byroute.Manager[*ResponseLimiter]
}

// NewResponseLimitByRoute creates a new per-route response limit manager.
func NewResponseLimitByRoute() *ResponseLimitByRoute {
	return &ResponseLimitByRoute{}
}

// AddRoute adds a response limiter for a route.
func (m *ResponseLimitByRoute) AddRoute(routeID string, cfg config.ResponseLimitConfig) {
	m.Add(routeID, New(cfg))
}

// GetLimiter returns the response limiter for a route.
func (m *ResponseLimitByRoute) GetLimiter(routeID string) *ResponseLimiter {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route response limit statistics.
func (m *ResponseLimitByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&m.Manager, func(rl *ResponseLimiter) Snapshot { return rl.Stats() })
}
