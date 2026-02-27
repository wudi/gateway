package etag

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/middleware/bufutil"
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
			bw := bufutil.New()

			// Copy existing headers from the real ResponseWriter so downstream
			// middleware that set headers before Write() are preserved.
			for k, v := range w.Header() {
				bw.Header()[k] = v
			}

			next.ServeHTTP(bw, r)

			// Only generate ETags for successful responses with a body.
			if bw.StatusCode >= 200 && bw.StatusCode < 300 && bw.Body.Len() > 0 {
				etag := generateETag(bw.Body.Bytes(), h.weak)
				bw.Header().Set("ETag", etag)
				h.generated.Add(1)

				// Check If-None-Match.
				if inm := r.Header.Get("If-None-Match"); inm != "" && matchETag(inm, etag) {
					h.notModified.Add(1)
					bufutil.CopyHeaders(w.Header(), bw.Header())
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}

			bw.FlushTo(w)
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

// ETagByRoute manages per-route ETag handlers.
type ETagByRoute = byroute.Factory[*ETagHandler, config.ETagConfig]

// NewETagByRoute creates a new per-route ETag handler manager.
func NewETagByRoute() *ETagByRoute {
	return byroute.SimpleFactory(New, func(h *ETagHandler) any {
		return map[string]interface{}{
			"generated":    h.Generated(),
			"not_modified": h.NotModified(),
		}
	})
}
