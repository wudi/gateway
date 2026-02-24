package etag

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware"
)

// ETagHandler generates ETag headers and handles If-None-Match conditional requests.
type ETagHandler struct {
	weak        bool
	generated   atomic.Int64
	notModified atomic.Int64
}

// New creates an ETagHandler from config.
func New(cfg config.ETagConfig) *ETagHandler {
	return &ETagHandler{
		weak: cfg.Weak,
	}
}

// Middleware returns a middleware that generates ETags and handles conditional requests.
func (h *ETagHandler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bufferingWriter{
				header: http.Header{},
				body:   &bytes.Buffer{},
				status: http.StatusOK,
			}

			// Copy existing headers from the real ResponseWriter so downstream
			// middleware that set headers before Write() are preserved.
			for k, v := range w.Header() {
				bw.header[k] = v
			}

			next.ServeHTTP(bw, r)

			// Only generate ETags for successful responses with a body.
			if bw.status >= 200 && bw.status < 300 && bw.body.Len() > 0 {
				etag := generateETag(bw.body.Bytes(), h.weak)
				bw.header.Set("ETag", etag)
				h.generated.Add(1)

				// Check If-None-Match.
				if inm := r.Header.Get("If-None-Match"); inm != "" && matchETag(inm, etag) {
					h.notModified.Add(1)
					// Copy headers to the real writer.
					copyHeaders(w.Header(), bw.header)
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}

			// Write buffered response to the real writer.
			copyHeaders(w.Header(), bw.header)
			w.WriteHeader(bw.status)
			if bw.body.Len() > 0 {
				w.Write(bw.body.Bytes())
			}
		})
	}
}

// Generated returns the number of ETags generated.
func (h *ETagHandler) Generated() int64 {
	return h.generated.Load()
}

// NotModified returns the number of 304 Not Modified responses sent.
func (h *ETagHandler) NotModified() int64 {
	return h.notModified.Load()
}

// bufferingWriter captures the response status and body for ETag computation.
type bufferingWriter struct {
	header http.Header
	body   *bytes.Buffer
	status int
}

func (bw *bufferingWriter) Header() http.Header {
	return bw.header
}

func (bw *bufferingWriter) Write(b []byte) (int, error) {
	return bw.body.Write(b)
}

func (bw *bufferingWriter) WriteHeader(status int) {
	bw.status = status
}

// Flush implements http.Flusher. Since we buffer everything, this is a no-op
// until the middleware flushes to the real writer.
func (bw *bufferingWriter) Flush() {}

// generateETag produces an ETag from body bytes using SHA-256.
func generateETag(body []byte, weak bool) string {
	sum := sha256.Sum256(body)
	tag := `"` + hex.EncodeToString(sum[:16]) + `"`
	if weak {
		tag = "W/" + tag
	}
	return tag
}

// matchETag checks whether the If-None-Match header value matches the given ETag.
// It supports the common cases: exact match, wildcard "*", and comma-separated lists.
func matchETag(inm, etag string) bool {
	if inm == "*" {
		return true
	}
	// Strip W/ prefix for weak comparison (RFC 7232 section 2.3.2).
	normalize := func(s string) string {
		if len(s) > 2 && s[:2] == "W/" {
			return s[2:]
		}
		return s
	}
	normETag := normalize(etag)

	// Parse comma-separated list of ETags.
	for inm != "" {
		// Skip leading whitespace and commas.
		for len(inm) > 0 && (inm[0] == ' ' || inm[0] == '\t' || inm[0] == ',') {
			inm = inm[1:]
		}
		if inm == "" {
			break
		}
		// Extract the next ETag value.
		var candidate string
		start := 0
		if len(inm) > 1 && inm[:2] == "W/" {
			start = 2
		}
		if start < len(inm) && inm[start] == '"' {
			// Find closing quote.
			end := start + 1
			for end < len(inm) && inm[end] != '"' {
				end++
			}
			if end < len(inm) {
				candidate = inm[:end+1]
				inm = inm[end+1:]
			} else {
				break
			}
		} else {
			break
		}
		if normalize(candidate) == normETag {
			return true
		}
	}
	return false
}

// copyHeaders copies all headers from src to dst.
func copyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// ETagByRoute manages per-route ETag handlers.
type ETagByRoute struct {
	byroute.Manager[*ETagHandler]
}

// NewETagByRoute creates a new per-route ETag handler manager.
func NewETagByRoute() *ETagByRoute {
	return &ETagByRoute{}
}

// AddRoute adds an ETag handler for a route.
func (m *ETagByRoute) AddRoute(routeID string, cfg config.ETagConfig) {
	m.Add(routeID, New(cfg))
}

// GetHandler returns the ETag handler for a route.
func (m *ETagByRoute) GetHandler(routeID string) *ETagHandler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route ETag stats.
func (m *ETagByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *ETagHandler) interface{} {
		return map[string]interface{}{
			"generated":    h.Generated(),
			"not_modified": h.NotModified(),
		}
	})
}
