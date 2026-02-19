package graphql

import (
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// GraphQLByRoute manages per-route GraphQL parsers.
type GraphQLByRoute struct {
	byroute.Manager[*Parser]
}

// NewGraphQLByRoute creates a new route-based GraphQL manager.
func NewGraphQLByRoute() *GraphQLByRoute {
	return &GraphQLByRoute{}
}

// AddRoute adds a GraphQL parser for a route.
func (m *GraphQLByRoute) AddRoute(routeID string, cfg config.GraphQLConfig) error {
	p, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, p)
	return nil
}

// GetParser returns the GraphQL parser for a route.
func (m *GraphQLByRoute) GetParser(routeID string) *Parser {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns stats from all per-route parsers.
func (m *GraphQLByRoute) Stats() map[string]interface{} {
	result := make(map[string]interface{})
	m.Range(func(id string, p *Parser) bool {
		result[id] = p.Stats()
		return true
	})
	return result
}
