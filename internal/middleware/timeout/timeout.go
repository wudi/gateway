package timeout

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/variables"
)

// CompiledTimeout holds pre-compiled timeout configuration for a route.
type CompiledTimeout struct {
	Request       time.Duration
	Idle          time.Duration
	Backend       time.Duration
	HeaderTimeout time.Duration
	retryAfter    string // pre-computed Retry-After header value (seconds)
	metrics       *TimeoutMetrics
}

// New creates a new CompiledTimeout from configuration.
func New(cfg config.TimeoutConfig) *CompiledTimeout {
	ct := &CompiledTimeout{
		Request:       cfg.Request,
		Idle:          cfg.Idle,
		Backend:       cfg.Backend,
		HeaderTimeout: cfg.HeaderTimeout,
		metrics:       &TimeoutMetrics{},
	}
	if cfg.Request > 0 {
		ct.retryAfter = fmt.Sprintf("%d", int(cfg.Request.Seconds()))
		if ct.retryAfter == "0" {
			ct.retryAfter = "1"
		}
	}
	return ct
}

// Metrics returns a snapshot of the timeout metrics.
func (ct *CompiledTimeout) Metrics() TimeoutSnapshot {
	return ct.metrics.Snapshot()
}

// Middleware returns an HTTP middleware that enforces the request-level timeout.
// It sets context.WithTimeout on the request context and wraps the ResponseWriter
// to inject a Retry-After header on 504 responses.
func (ct *CompiledTimeout) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		// If no request timeout is configured, pass through
		if ct.Request <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ct.metrics.TotalRequests.Add(1)

			timeout := ct.Request
			if varCtx := variables.GetFromRequest(r); varCtx.Overrides != nil && varCtx.Overrides.TimeoutOverride > 0 {
				// Override can only tighten, not loosen
				if varCtx.Overrides.TimeoutOverride < timeout {
					timeout = varCtx.Overrides.TimeoutOverride
				}
			}

			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			rw := &retryAfterWriter{
				ResponseWriter: w,
				retryAfter:     ct.retryAfter,
			}
			next.ServeHTTP(rw, r.WithContext(ctx))

			// Check if the context deadline was exceeded (timeout)
			if ctx.Err() == context.DeadlineExceeded {
				ct.metrics.RequestTimeouts.Add(1)
			}
		})
	}
}

// retryAfterWriter intercepts WriteHeader to inject a Retry-After header on 504 responses.
type retryAfterWriter struct {
	http.ResponseWriter
	retryAfter    string
	headerWritten bool
}

func (w *retryAfterWriter) WriteHeader(code int) {
	if !w.headerWritten {
		w.headerWritten = true
		if code == http.StatusGatewayTimeout && w.retryAfter != "" {
			w.ResponseWriter.Header().Set("Retry-After", w.retryAfter)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *retryAfterWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *retryAfterWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// TimeoutStatus describes the timeout configuration and metrics for a route.
type TimeoutStatus struct {
	Request       string          `json:"request,omitempty"`
	Idle          string          `json:"idle,omitempty"`
	Backend       string          `json:"backend,omitempty"`
	HeaderTimeout string          `json:"header_timeout,omitempty"`
	Metrics       TimeoutSnapshot `json:"metrics"`
}

// Status returns the admin API snapshot for this timeout configuration.
func (ct *CompiledTimeout) Status() TimeoutStatus {
	s := TimeoutStatus{Metrics: ct.Metrics()}
	if ct.Request > 0 {
		s.Request = ct.Request.String()
	}
	if ct.Idle > 0 {
		s.Idle = ct.Idle.String()
	}
	if ct.Backend > 0 {
		s.Backend = ct.Backend.String()
	}
	if ct.HeaderTimeout > 0 {
		s.HeaderTimeout = ct.HeaderTimeout.String()
	}
	return s
}

// TimeoutByRoute manages per-route compiled timeouts.
type TimeoutByRoute = byroute.Factory[*CompiledTimeout, config.TimeoutConfig]

// NewTimeoutByRoute creates a new timeout manager.
func NewTimeoutByRoute() *TimeoutByRoute {
	return byroute.SimpleFactory(New, func(ct *CompiledTimeout) any {
		return ct.Status()
	})
}
