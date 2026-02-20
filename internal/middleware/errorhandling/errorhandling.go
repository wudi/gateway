package errorhandling

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/variables"
)

// ErrorHandler reformats error responses based on a configured mode.
type ErrorHandler struct {
	mode        string
	total       atomic.Int64
	reformatted atomic.Int64
}

// New creates an ErrorHandler from config.
func New(cfg config.ErrorHandlingConfig) *ErrorHandler {
	mode := cfg.Mode
	if mode == "" {
		mode = "default"
	}
	return &ErrorHandler{
		mode: mode,
	}
}

// Middleware returns a middleware that reformats error responses.
func (h *ErrorHandler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		// "default" mode does nothing — pass through directly.
		if h.mode == "default" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bufferingWriter{
				ResponseWriter: w,
				buf:            &bytes.Buffer{},
			}
			next.ServeHTTP(bw, r)

			h.total.Add(1)

			// Non-error status codes pass through unchanged.
			if bw.statusCode < 400 {
				w.WriteHeader(bw.statusCode)
				w.Write(bw.buf.Bytes())
				return
			}

			h.reformatted.Add(1)

			var body []byte
			switch h.mode {
			case "pass_status":
				body = mustMarshal(map[string]interface{}{
					"error":  "gateway error",
					"status": bw.statusCode,
				})
			case "detailed":
				routeID := "unknown"
				if vc := variables.GetFromRequest(r); vc != nil && vc.RouteID != "" {
					routeID = vc.RouteID
				}
				key := fmt.Sprintf("error_%s", routeID)
				body = mustMarshal(map[string]interface{}{
					key: map[string]interface{}{
						"status": bw.statusCode,
						"body":   bw.buf.String(),
					},
				})
			case "message":
				body = mustMarshal(map[string]interface{}{
					"message": "backend returned error",
					"status":  bw.statusCode,
				})
				// Override status to 200 for "message" mode.
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				w.WriteHeader(http.StatusOK)
				w.Write(body)
				return
			default:
				// Unknown mode — pass through.
				w.WriteHeader(bw.statusCode)
				w.Write(bw.buf.Bytes())
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(bw.statusCode)
			w.Write(body)
		})
	}
}

// Stats returns error handling statistics.
func (h *ErrorHandler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"mode":        h.mode,
		"total":       h.total.Load(),
		"reformatted": h.reformatted.Load(),
	}
}

// bufferingWriter captures the status code and response body so the middleware
// can decide whether to reformat the response.
type bufferingWriter struct {
	http.ResponseWriter
	buf         *bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func (w *bufferingWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
}

func (w *bufferingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.buf.Write(b)
}

func (w *bufferingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *bufferingWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// mustMarshal marshals v to JSON, panicking on error (should not happen with
// simple map types).
func mustMarshal(v interface{}) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("errorhandling: json.Marshal: %v", err))
	}
	return b
}

// ErrorHandlerByRoute manages per-route error handlers.
type ErrorHandlerByRoute struct {
	byroute.Manager[*ErrorHandler]
}

// NewErrorHandlerByRoute creates a new per-route error handler manager.
func NewErrorHandlerByRoute() *ErrorHandlerByRoute {
	return &ErrorHandlerByRoute{}
}

// AddRoute adds an error handler for a route.
func (m *ErrorHandlerByRoute) AddRoute(routeID string, cfg config.ErrorHandlingConfig) {
	m.Add(routeID, New(cfg))
}

// GetHandler returns the error handler for a route.
func (m *ErrorHandlerByRoute) GetHandler(routeID string) *ErrorHandler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route error handling stats.
func (m *ErrorHandlerByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *ErrorHandler) interface{} { return h.Stats() })
}
