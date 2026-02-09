package graphql

import (
	"sync"

	"github.com/wudi/gateway/internal/config"
)

// GraphQLByRoute manages per-route GraphQL parsers.
type GraphQLByRoute struct {
	parsers map[string]*Parser
	mu      sync.RWMutex
}

// NewGraphQLByRoute creates a new route-based GraphQL manager.
func NewGraphQLByRoute() *GraphQLByRoute {
	return &GraphQLByRoute{
		parsers: make(map[string]*Parser),
	}
}

// AddRoute adds a GraphQL parser for a route.
func (m *GraphQLByRoute) AddRoute(routeID string, cfg config.GraphQLConfig) error {
	p, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.parsers[routeID] = p
	m.mu.Unlock()
	return nil
}

// GetParser returns the GraphQL parser for a route.
func (m *GraphQLByRoute) GetParser(routeID string) *Parser {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.parsers[routeID]
}

// RouteIDs returns all route IDs with GraphQL parsers.
func (m *GraphQLByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.parsers))
	for id := range m.parsers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns stats from all per-route parsers.
func (m *GraphQLByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]interface{}, len(m.parsers))
	for id, p := range m.parsers {
		result[id] = p.Stats()
	}
	return result
}
