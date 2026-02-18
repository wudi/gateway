package protocol

import (
	"net/http"
	"sync"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

// translatorRoute holds per-route translator state.
type translatorRoute struct {
	handler    http.Handler
	translator Translator
	cfg        config.ProtocolConfig
}

// TranslatorByRoute manages protocol translators per route.
type TranslatorByRoute struct {
	routes map[string]*translatorRoute
	mu     sync.RWMutex
}

// NewTranslatorByRoute creates a new TranslatorByRoute manager.
func NewTranslatorByRoute() *TranslatorByRoute {
	return &TranslatorByRoute{}
}

// AddRoute sets up a protocol translator for the given route.
func (m *TranslatorByRoute) AddRoute(routeID string, cfg config.ProtocolConfig, balancer loadbalancer.Balancer) error {
	translator, err := New(cfg.Type)
	if err != nil {
		return err
	}

	handler, err := translator.Handler(routeID, balancer, cfg)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Close existing translator if replacing
	if existing, ok := m.routes[routeID]; ok {
		existing.translator.Close(routeID)
	}

	if m.routes == nil {
		m.routes = make(map[string]*translatorRoute)
	}
	m.routes[routeID] = &translatorRoute{
		handler:    handler,
		translator: translator,
		cfg:        cfg,
	}
	return nil
}

// GetHandler returns the http.Handler for the route, or nil if not configured.
func (m *TranslatorByRoute) GetHandler(routeID string) http.Handler {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if route, ok := m.routes[routeID]; ok {
		return route.handler
	}
	return nil
}

// HasRoute returns true if the route has a protocol translator configured.
func (m *TranslatorByRoute) HasRoute(routeID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.routes[routeID]
	return ok
}

// RouteIDs returns a list of route IDs with protocol translators.
func (m *TranslatorByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.routes))
	for id := range m.routes {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns metrics for all routes.
func (m *TranslatorByRoute) Stats() map[string]*TranslatorMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stats := make(map[string]*TranslatorMetrics, len(m.routes))
	for routeID, route := range m.routes {
		if metrics := route.translator.Metrics(routeID); metrics != nil {
			stats[routeID] = metrics
		}
	}
	return stats
}

// Close releases resources for all routes.
func (m *TranslatorByRoute) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for routeID, route := range m.routes {
		route.translator.Close(routeID)
	}
	m.routes = make(map[string]*translatorRoute)
}
