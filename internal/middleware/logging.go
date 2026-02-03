package middleware

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/example/gateway/internal/variables"
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

	logger := log.New(cfg.Output, "", 0)

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

			if cfg.JSON {
				entry := LogEntry{
					Timestamp:    time.Now().Format(time.RFC3339),
					RequestID:    varCtx.RequestID,
					RemoteAddr:   variables.ExtractClientIP(r),
					Method:       r.Method,
					Path:         r.URL.Path,
					Query:        r.URL.RawQuery,
					Status:       lrw.status,
					BodyBytes:    lrw.bytes,
					UserAgent:    r.UserAgent(),
					ResponseTime: duration.String(),
					RouteID:      varCtx.RouteID,
					UpstreamAddr: varCtx.UpstreamAddr,
				}
				if varCtx.Identity != nil {
					entry.AuthClientID = varCtx.Identity.ClientID
				}

				jsonBytes, _ := json.Marshal(entry)
				logger.Println(string(jsonBytes))
			} else {
				// Use format string with variable interpolation
				logLine := resolver.Resolve(cfg.Format, varCtx)
				logger.Println(logLine)
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
