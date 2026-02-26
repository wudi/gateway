package middleware

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/wudi/runway/variables"
)

func init() {
	// Batch crypto/rand reads into a pool to avoid a syscall per UUID.
	uuid.EnableRandPool()
}

// RequestIDConfig configures the request ID middleware
type RequestIDConfig struct {
	// Header is the header name to use for the request ID
	Header string
	// Generator generates a new request ID
	Generator func() string
	// TrustHeader trusts incoming request ID headers
	TrustHeader bool
}

// DefaultRequestIDConfig provides default request ID settings
var DefaultRequestIDConfig = RequestIDConfig{
	Header:      "X-Request-ID",
	Generator:   defaultIDGenerator,
	TrustHeader: true,
}

func defaultIDGenerator() string {
	return uuid.New().String()
}

// RequestID creates a request ID middleware with default config
func RequestID() Middleware {
	return RequestIDWithConfig(DefaultRequestIDConfig)
}

// RequestIDWithConfig creates a request ID middleware with custom config
func RequestIDWithConfig(cfg RequestIDConfig) Middleware {
	if cfg.Header == "" {
		cfg.Header = "X-Request-ID"
	}
	if cfg.Generator == nil {
		cfg.Generator = defaultIDGenerator
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var requestID string

			// Check for existing request ID if trusted
			if cfg.TrustHeader {
				requestID = r.Header.Get(cfg.Header)
			}

			// Generate new ID if not present
			if requestID == "" {
				requestID = cfg.Generator()
			}

			// Set request ID in request header
			r.Header.Set(cfg.Header, requestID)

			// Set request ID in response header
			w.Header().Set(cfg.Header, requestID)

			// Add to context
			varCtx := variables.GetFromRequest(r)
			varCtx.RequestID = requestID
			ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)

			next.ServeHTTP(w, r.WithContext(ctx))
			variables.ReleaseContext(varCtx)
		})
	}
}

// GetRequestID extracts the request ID from the request context
func GetRequestID(r *http.Request) string {
	varCtx := variables.GetFromRequest(r)
	return varCtx.RequestID
}

// requestIDKey is the context key for request ID (for backward compatibility)
type requestIDKey struct{}

// WithRequestID adds a request ID to the context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

// RequestIDFromContext extracts the request ID from context
func RequestIDFromContext(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	// Also check variable context
	if varCtx, ok := ctx.Value(variables.RequestContextKey{}).(*variables.Context); ok {
		return varCtx.RequestID
	}
	return ""
}
