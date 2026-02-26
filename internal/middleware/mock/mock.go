package mock

import (
	"net/http"
	"sync/atomic"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/middleware/mock/specmock"
)

// Handler is a mock response handler that can serve static or spec-based responses.
type Handler interface {
	Middleware() middleware.Middleware
	Served() int64
}

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
	byroute.Manager[Handler]
}

// NewMockByRoute creates a new per-route mock handler manager.
func NewMockByRoute() *MockByRoute {
	return &MockByRoute{}
}

// AddRoute adds a mock handler for a route.
func (m *MockByRoute) AddRoute(routeID string, cfg config.MockResponseConfig) {
	m.Add(routeID, New(cfg))
}

// AddSpecRoute adds a spec-based mock handler for a route.
func (m *MockByRoute) AddSpecRoute(routeID string, cfg config.MockResponseConfig, doc *openapi3.T) {
	m.Add(routeID, specmock.New(doc, cfg.DefaultStatus, cfg.Seed, cfg.Headers))
}

// GetHandler returns the mock handler for a route.
func (m *MockByRoute) GetHandler(routeID string) Handler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route mock stats.
func (m *MockByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h Handler) interface{} {
		result := map[string]interface{}{"served": h.Served()}
		if _, ok := h.(*specmock.SpecMocker); ok {
			result["from_spec"] = true
		}
		return result
	})
}
