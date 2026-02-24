package grpc

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/config"
)

// IsGRPCRequest checks if the request is a gRPC request
func IsGRPCRequest(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/grpc")
}

// Handler wraps a proxy handler to support gRPC-specific behavior
type Handler struct {
	enabled             bool
	deadlinePropagation bool
	maxRecvMsgSize      int
	maxSendMsgSize      int
	authority           string
	metadata            *MetadataTransformer
	healthChecker       *HealthChecker

	// metrics
	requests     atomic.Int64
	deadlinesSet atomic.Int64
}

// New creates a new gRPC handler from config
func New(cfg config.GRPCConfig) *Handler {
	h := &Handler{
		enabled:             cfg.Enabled,
		deadlinePropagation: cfg.DeadlinePropagation,
		maxRecvMsgSize:      cfg.MaxRecvMsgSize,
		maxSendMsgSize:      cfg.MaxSendMsgSize,
		authority:           cfg.Authority,
	}

	mt := NewMetadataTransformer(cfg.MetadataTransforms)
	if mt.HasTransforms() {
		h.metadata = mt
	}

	if cfg.HealthCheck.Enabled {
		h.healthChecker = NewHealthChecker(cfg.HealthCheck.Service)
	}

	return h
}

// IsEnabled returns whether gRPC handling is enabled
func (h *Handler) IsEnabled() bool {
	return h.enabled
}

// GetHealthChecker returns the gRPC health checker, or nil if not configured.
func (h *Handler) GetHealthChecker() *HealthChecker {
	return h.healthChecker
}

// PrepareRequest prepares an HTTP/2 request for gRPC proxying.
// Returns a cancel func that must be deferred by the caller.
func (h *Handler) PrepareRequest(r *http.Request) (*http.Request, context.CancelFunc) {
	if !h.enabled {
		return r, func() {}
	}

	h.requests.Add(1)

	// Ensure HTTP/2 is used (gRPC requires it)
	r.Proto = "HTTP/2.0"
	r.ProtoMajor = 2
	r.ProtoMinor = 0

	// TE: trailers is required for gRPC
	r.Header.Set("TE", "trailers")

	// Override :authority if configured
	if h.authority != "" {
		r.Host = h.authority
	}

	// Apply request metadata transforms
	if h.metadata != nil {
		h.metadata.TransformRequest(r)
	}

	// Apply request body size limit
	if h.maxRecvMsgSize > 0 {
		LimitRequestBody(r, h.maxRecvMsgSize)
	}

	// Deadline propagation
	cancel := func() {}
	if h.deadlinePropagation {
		r, cancel = PropagateDeadline(r)
		if _, hasDeadline := r.Context().Deadline(); hasDeadline {
			h.deadlinesSet.Add(1)
			SetRemainingTimeout(r)
		}
	}

	return r, cancel
}

// ProcessResponse applies response-side transforms.
func (h *Handler) ProcessResponse(w http.ResponseWriter) {
	if h.metadata != nil {
		h.metadata.TransformResponse(w)
	}
}

// WrapResponseWriter wraps the response writer with send size enforcement if configured.
// Returns the original writer if no limit is set.
func (h *Handler) WrapResponseWriter(w http.ResponseWriter) http.ResponseWriter {
	if h.maxSendMsgSize <= 0 {
		return w
	}
	return WrapResponseWriter(w, h.maxSendMsgSize)
}

// Stats returns handler statistics.
func (h *Handler) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"enabled":              h.enabled,
		"deadline_propagation": h.deadlinePropagation,
		"requests":             h.requests.Load(),
		"deadlines_set":        h.deadlinesSet.Load(),
	}
	if h.maxRecvMsgSize > 0 {
		stats["max_recv_msg_size"] = h.maxRecvMsgSize
	}
	if h.maxSendMsgSize > 0 {
		stats["max_send_msg_size"] = h.maxSendMsgSize
	}
	if h.authority != "" {
		stats["authority"] = h.authority
	}
	if h.healthChecker != nil {
		stats["health_check"] = true
	}
	return stats
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
