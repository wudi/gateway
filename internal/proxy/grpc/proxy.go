package grpc

import (
	"net/http"
	"strings"
)

// IsGRPCRequest checks if the request is a gRPC request
func IsGRPCRequest(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/grpc")
}

// Handler wraps a proxy handler to support gRPC-specific behavior
type Handler struct {
	enabled bool
}

// New creates a new gRPC handler
func New(enabled bool) *Handler {
	return &Handler{enabled: enabled}
}

// IsEnabled returns whether gRPC handling is enabled
func (h *Handler) IsEnabled() bool {
	return h.enabled
}

// PrepareRequest prepares an HTTP/2 request for gRPC proxying
func (h *Handler) PrepareRequest(r *http.Request) {
	if !h.enabled {
		return
	}

	// Ensure HTTP/2 is used (gRPC requires it)
	r.Proto = "HTTP/2.0"
	r.ProtoMajor = 2
	r.ProtoMinor = 0

	// Preserve gRPC-specific headers
	// TE: trailers is required for gRPC
	r.Header.Set("TE", "trailers")
}

// CopyTrailers copies gRPC trailers from the response
func CopyTrailers(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Trailer {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
}

// MapStatusCode maps gRPC status codes for circuit breaker compatibility
// gRPC uses Grpc-Status header: 0=OK, non-zero=failure
func MapStatusCode(r *http.Response) int {
	if r == nil {
		return 503
	}

	// For gRPC, HTTP status 200 doesn't necessarily mean success
	// Check the Grpc-Status trailer/header
	grpcStatus := r.Header.Get("Grpc-Status")
	if grpcStatus == "" {
		// Check trailers
		grpcStatus = r.Trailer.Get("Grpc-Status")
	}

	switch grpcStatus {
	case "", "0":
		return r.StatusCode // OK
	case "1": // CANCELLED
		return 499
	case "2": // UNKNOWN
		return 500
	case "3": // INVALID_ARGUMENT
		return 400
	case "4": // DEADLINE_EXCEEDED
		return 504
	case "5": // NOT_FOUND
		return 404
	case "7": // PERMISSION_DENIED
		return 403
	case "8": // RESOURCE_EXHAUSTED
		return 429
	case "12": // UNIMPLEMENTED
		return 501
	case "13": // INTERNAL
		return 500
	case "14": // UNAVAILABLE
		return 503
	default:
		return 500
	}
}

// IsRetryableGRPCStatus checks if a gRPC status should be retried
func IsRetryableGRPCStatus(grpcStatus string) bool {
	switch grpcStatus {
	case "14": // UNAVAILABLE
		return true
	case "8": // RESOURCE_EXHAUSTED
		return true
	default:
		return false
	}
}
