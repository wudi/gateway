package middleware

import (
	"io"
	"net/http"
	"os"
	"time"

	"github.com/example/gateway/internal/logging"
	"github.com/example/gateway/internal/middleware/accesslog"
	"github.com/example/gateway/internal/variables"
	"go.uber.org/zap"
)

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
			lrw := &loggingResponseWriter{
				ResponseWriter: w,
				status:         http.StatusOK,
			}

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
				fields := []zap.Field{
					zap.String("request_id", varCtx.RequestID),
					zap.String("remote_addr", variables.ExtractClientIP(r)),
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.Int("status", lrw.status),
					zap.Int64("body_bytes", lrw.bytes),
					zap.Duration("response_time", duration),
				}
				if r.URL.RawQuery != "" {
					fields = append(fields, zap.String("query", r.URL.RawQuery))
				}
				if varCtx.RouteID != "" {
					fields = append(fields, zap.String("route_id", varCtx.RouteID))
				}
				if varCtx.UpstreamAddr != "" {
					fields = append(fields, zap.String("upstream_addr", varCtx.UpstreamAddr))
				}
				if varCtx.Identity != nil {
					fields = append(fields, zap.String("auth_client_id", varCtx.Identity.ClientID))
				}
				if ua := r.UserAgent(); ua != "" {
					fields = append(fields, zap.String("user_agent", ua))
				}

				// Per-route header capture
				if alCfg != nil && alCfg.HasHeaderCapture() {
					reqHeaders := alCfg.CaptureRequestHeaders(r)
					if len(reqHeaders) > 0 {
						fields = append(fields, zap.Any("request_headers", reqHeaders))
					}
					respHeaders := alCfg.CaptureResponseHeaders(lrw.Header())
					if len(respHeaders) > 0 {
						fields = append(fields, zap.Any("response_headers", respHeaders))
					}
				}

				// Per-route body capture
				if reqBody, ok := varCtx.Custom["_al_req_body"]; ok && reqBody != "" {
					fields = append(fields, zap.String("request_body", reqBody))
				}
				if respBody, ok := varCtx.Custom["_al_resp_body"]; ok && respBody != "" {
					fields = append(fields, zap.String("response_body", respBody))
				}

				logging.Info("HTTP request", fields...)
			} else {
				// Use format string with variable interpolation
				logLine := resolver.Resolve(format, varCtx)
				logging.Info(logLine)
			}
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
