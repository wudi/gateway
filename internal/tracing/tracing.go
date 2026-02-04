package tracing

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/middleware"
)

// Tracer provides distributed tracing functionality
type Tracer struct {
	enabled     bool
	exporter    string
	endpoint    string
	serviceName string
	sampleRate  float64
}

// New creates a new Tracer from config
func New(cfg config.TracingConfig) *Tracer {
	t := &Tracer{
		enabled:     cfg.Enabled,
		exporter:    cfg.Exporter,
		endpoint:    cfg.Endpoint,
		serviceName: cfg.ServiceName,
		sampleRate:  cfg.SampleRate,
	}

	if t.serviceName == "" {
		t.serviceName = "api-gateway"
	}
	if t.sampleRate <= 0 {
		t.sampleRate = 1.0
	}

	return t
}

// IsEnabled returns whether tracing is enabled
func (t *Tracer) IsEnabled() bool {
	return t.enabled
}

// Middleware returns a middleware that propagates W3C trace context headers
func (t *Tracer) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !t.enabled {
				next.ServeHTTP(w, r)
				return
			}

			// Check for existing traceparent header
			traceparent := r.Header.Get("traceparent")
			if traceparent == "" {
				// Generate a new trace context
				traceparent = generateTraceparent()
				r.Header.Set("traceparent", traceparent)
			}

			// Propagate tracestate if present
			// tracestate is forwarded as-is

			// Set response headers for tracing
			w.Header().Set("X-Trace-ID", extractTraceID(traceparent))

			next.ServeHTTP(w, r)
		})
	}
}

// generateTraceparent generates a W3C traceparent header
// Format: version-traceid-parentid-traceflags
func generateTraceparent() string {
	traceID := make([]byte, 16)
	parentID := make([]byte, 8)
	rand.Read(traceID)
	rand.Read(parentID)
	return fmt.Sprintf("00-%s-%s-01", hex.EncodeToString(traceID), hex.EncodeToString(parentID))
}

// extractTraceID extracts the trace ID from a traceparent header
func extractTraceID(traceparent string) string {
	// Format: version-traceid-parentid-traceflags
	if len(traceparent) < 55 {
		return ""
	}
	return traceparent[3:35]
}

// InjectHeaders injects trace context headers into an outgoing request
func InjectHeaders(src, dst *http.Request) {
	if tp := src.Header.Get("traceparent"); tp != "" {
		dst.Header.Set("traceparent", tp)
	}
	if ts := src.Header.Get("tracestate"); ts != "" {
		dst.Header.Set("tracestate", ts)
	}
}

// Close shuts down the tracer
func (t *Tracer) Close() error {
	return nil
}
