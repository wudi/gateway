package statusmap

import (
	"net/http"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/internal/middleware"
)

// StatusMapper remaps backend response status codes to different client-facing codes.
type StatusMapper struct {
	routeID  string
	mappings map[int]int
	total    atomic.Int64
	remapped atomic.Int64
}

// New creates a StatusMapper from a mappings config.
func New(routeID string, mappings map[int]int) *StatusMapper {
	// Copy the map to prevent external mutation.
	m := make(map[int]int, len(mappings))
	for k, v := range mappings {
		m[k] = v
	}
	return &StatusMapper{
		routeID:  routeID,
		mappings: m,
	}
}

// Middleware returns a middleware that remaps response status codes.
func (s *StatusMapper) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mw := &mappingWriter{
				ResponseWriter: w,
				mapper:         s,
			}
			next.ServeHTTP(mw, r)
		})
	}
}

// Stats returns status mapping statistics.
func (s *StatusMapper) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total":    s.total.Load(),
		"remapped": s.remapped.Load(),
		"mappings": s.mappings,
	}
}

// mappingWriter wraps http.ResponseWriter to remap status codes.
type mappingWriter struct {
	http.ResponseWriter
	mapper      *StatusMapper
	wroteHeader bool
}

func (w *mappingWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.mapper.total.Add(1)
	if mapped, ok := w.mapper.mappings[code]; ok {
		w.mapper.remapped.Add(1)
		w.ResponseWriter.WriteHeader(mapped)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *mappingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *mappingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *mappingWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// StatusMapByRoute manages per-route status mappers.
type StatusMapByRoute struct {
	byroute.Manager[*StatusMapper]
}

// NewStatusMapByRoute creates a new per-route status map manager.
func NewStatusMapByRoute() *StatusMapByRoute {
	return &StatusMapByRoute{}
}

// AddRoute adds a status mapper for a route.
func (m *StatusMapByRoute) AddRoute(routeID string, mappings map[int]int) {
	m.Add(routeID, New(routeID, mappings))
}

// GetMapper returns the status mapper for a route.
func (m *StatusMapByRoute) GetMapper(routeID string) *StatusMapper {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route status mapping stats.
func (m *StatusMapByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(sm *StatusMapper) interface{} { return sm.Stats() })
}
