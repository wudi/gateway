package mock

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
)

// MockHandler returns static mock responses without reaching the backend.
type MockHandler struct {
	statusCode  int
	headers     map[string]string
	body        []byte
	served      atomic.Int64
}

// New creates a MockHandler from config.
func New(cfg config.MockResponseConfig) *MockHandler {
	status := cfg.StatusCode
	if status == 0 {
		status = 200
	}
	return &MockHandler{
		statusCode: status,
		headers:    cfg.Headers,
		body:       []byte(cfg.Body),
	}
}

// Middleware returns a middleware that always returns the mock response.
func (m *MockHandler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.served.Add(1)
			for k, v := range m.headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(m.statusCode)
			if len(m.body) > 0 {
				w.Write(m.body)
			}
		})
	}
}

// Served returns the number of mock responses served.
func (m *MockHandler) Served() int64 {
	return m.served.Load()
}

// MockByRoute manages per-route mock handlers.
type MockByRoute struct {
	handlers map[string]*MockHandler
	mu       sync.RWMutex
}

// NewMockByRoute creates a new per-route mock handler manager.
func NewMockByRoute() *MockByRoute {
	return &MockByRoute{}
}

// AddRoute adds a mock handler for a route.
func (m *MockByRoute) AddRoute(routeID string, cfg config.MockResponseConfig) {
	m.mu.Lock()
	if m.handlers == nil {
		m.handlers = make(map[string]*MockHandler)
	}
	m.handlers[routeID] = New(cfg)
	m.mu.Unlock()
}

// GetHandler returns the mock handler for a route.
func (m *MockByRoute) GetHandler(routeID string) *MockHandler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handlers[routeID]
}

// RouteIDs returns all route IDs with mock handlers.
func (m *MockByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.handlers))
	for id := range m.handlers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route mock stats.
func (m *MockByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[string]interface{}, len(m.handlers))
	for id, h := range m.handlers {
		stats[id] = map[string]interface{}{
			"served": h.Served(),
		}
	}
	return stats
}
