package middleware

import (
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware/accesslog"
	"github.com/wudi/gateway/variables"
	"go.uber.org/zap"
)

var loggingRWPool = sync.Pool{
	New: func() any { return &loggingResponseWriter{} },
}

// LoggingConfig configures the logging middleware
type LoggingConfig struct {
	// Format is the log format string with variables
	Format string
	// Output is the writer to write logs to
	Output io.Writer
	// SkipPaths are paths that should not be logged
	SkipPaths []string
	// JSON enables JSON logging
	JSON bool
}

// DefaultLoggingConfig provides default logging settings
var DefaultLoggingConfig = LoggingConfig{
	Format: `$remote_addr - [$time_iso8601] "$request_method $request_uri" $status $body_bytes_sent "$http_user_agent" $response_time`,
	Output: os.Stdout,
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp    string `json:"timestamp"`
	RequestID    string `json:"request_id,omitempty"`
	RemoteAddr   string `json:"remote_addr"`
	Method       string `json:"method"`
	Path         string `json:"path"`
	Query        string `json:"query,omitempty"`
	Status       int    `json:"status"`
	BodyBytes    int64  `json:"body_bytes"`
	UserAgent    string `json:"user_agent,omitempty"`
	ResponseTime string `json:"response_time"`
	RouteID      string `json:"route_id,omitempty"`
	UpstreamAddr string `json:"upstream_addr,omitempty"`
	AuthClientID string `json:"auth_client_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Logging creates a logging middleware with default config
func Logging() Middleware {
	return LoggingWithConfig(DefaultLoggingConfig)
}

// LoggingWithConfig creates a logging middleware with custom config
func LoggingWithConfig(cfg LoggingConfig) Middleware {
	if cfg.Output == nil {
		cfg.Output = os.Stdout
	}

	resolver := variables.NewResolver()
	skipPaths := make(map[string]bool)
	for _, p := range cfg.SkipPaths {
		skipPaths[p] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip logging for certain paths
			if skipPaths[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}

			start := time.Now()

			// Wrap response writer to capture status and bytes
			lrw := loggingRWPool.Get().(*loggingResponseWriter)
			lrw.ResponseWriter = w
			lrw.status = http.StatusOK
			lrw.bytes = 0

			// Process request
			next.ServeHTTP(lrw, r)

			// Calculate response time
			duration := time.Since(start)

			// Get or create variable context
			varCtx := variables.GetFromRequest(r)
			varCtx.Status = lrw.status
			varCtx.BodyBytesSent = lrw.bytes
			varCtx.ResponseTime = duration

			// Per-route access log overrides
			var alCfg *accesslog.CompiledAccessLog
			if varCtx.AccessLogConfig != nil {
				alCfg, _ = varCtx.AccessLogConfig.(*accesslog.CompiledAccessLog)
			}

			// Check if logging is disabled for this route
			if alCfg != nil && alCfg.Enabled != nil && !*alCfg.Enabled {
				return
			}

			// Check conditional logging (status codes, methods, sampling)
			if alCfg != nil && !alCfg.ShouldLog(lrw.status, r.Method) {
				return
			}

			// Determine format (per-route or global)
			format := cfg.Format
			if alCfg != nil && alCfg.Format != "" {
				format = alCfg.Format
			}

			if cfg.JSON {
				// Stack-allocated array avoids slice growth allocations.
				var fields [16]zap.Field
				n := 0
				fields[n] = zap.String("request_id", varCtx.RequestID); n++
				fields[n] = zap.String("remote_addr", variables.ExtractClientIP(r)); n++
				fields[n] = zap.String("method", r.Method); n++
				fields[n] = zap.String("path", r.URL.Path); n++
				fields[n] = zap.Int("status", lrw.status); n++
				fields[n] = zap.Int64("body_bytes", lrw.bytes); n++
				fields[n] = zap.Duration("response_time", duration); n++
				if r.URL.RawQuery != "" {
					fields[n] = zap.String("query", r.URL.RawQuery); n++
				}
				if varCtx.RouteID != "" {
					fields[n] = zap.String("route_id", varCtx.RouteID); n++
				}
				if varCtx.UpstreamAddr != "" {
					fields[n] = zap.String("upstream_addr", varCtx.UpstreamAddr); n++
				}
				if varCtx.TenantID != "" {
					fields[n] = zap.String("tenant_id", varCtx.TenantID); n++
				}
				if varCtx.Identity != nil {
					fields[n] = zap.String("auth_client_id", varCtx.Identity.ClientID); n++
				}
				if ua := r.UserAgent(); ua != "" {
					fields[n] = zap.String("user_agent", ua); n++
				}

				// Per-route header/body capture may exceed the fixed array; use append for overflow.
				extra := fields[:n]
				if alCfg != nil && alCfg.HasHeaderCapture() {
					reqHeaders := alCfg.CaptureRequestHeaders(r)
					if len(reqHeaders) > 0 {
						extra = append(extra, zap.Any("request_headers", reqHeaders))
					}
					respHeaders := alCfg.CaptureResponseHeaders(lrw.Header())
					if len(respHeaders) > 0 {
						extra = append(extra, zap.Any("response_headers", respHeaders))
					}
				}
				if reqBody, ok := varCtx.Custom["_al_req_body"]; ok && reqBody != "" {
					extra = append(extra, zap.String("request_body", reqBody))
				}
				if respBody, ok := varCtx.Custom["_al_resp_body"]; ok && respBody != "" {
					extra = append(extra, zap.String("response_body", respBody))
				}

				logging.Info("HTTP request", extra...)
			} else {
				// Use format string with variable interpolation
				logLine := resolver.Resolve(format, varCtx)
				logging.Info(logLine)
			}

			lrw.ResponseWriter = nil
			loggingRWPool.Put(lrw)
		})
	}
}

// loggingResponseWriter wraps http.ResponseWriter to capture status and bytes
type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (lrw *loggingResponseWriter) WriteHeader(status int) {
	lrw.status = status
	lrw.ResponseWriter.WriteHeader(status)
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := lrw.ResponseWriter.Write(b)
	lrw.bytes += int64(n)
	return n, err
}

// Flush implements http.Flusher
func (lrw *loggingResponseWriter) Flush() {
	if f, ok := lrw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker
func (lrw *loggingResponseWriter) Hijack() (interface{}, interface{}, error) {
	if h, ok := lrw.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Status returns the recorded status code
func (lrw *loggingResponseWriter) Status() int {
	return lrw.status
}

// BytesWritten returns the number of bytes written
func (lrw *loggingResponseWriter) BytesWritten() int64 {
	return lrw.bytes
}
