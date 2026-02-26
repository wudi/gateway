package federation

import (
	"context"
	"net/http"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// FederationByRoute manages per-route federation stitchers and handlers.
type FederationByRoute struct {
	byroute.Manager[*Handler]
	stitchers byroute.Manager[*Stitcher]
}

// NewFederationByRoute creates a new per-route federation manager.
func NewFederationByRoute() *FederationByRoute {
	return &FederationByRoute{}
}

// AddRoute adds a federation handler for a route.
func (m *FederationByRoute) AddRoute(routeID string, cfg config.GraphQLFederationConfig, transport http.RoundTripper) error {
	stitcher := NewStitcher(routeID, cfg, transport)

	// Perform initial schema introspection with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := stitcher.Init(ctx); err != nil {
		return err
	}

	handler := NewHandler(stitcher)
	m.Add(routeID, handler)
	m.stitchers.Add(routeID, stitcher)
	return nil
}

// GetHandler returns the federation HTTP handler for a route.
func (m *FederationByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route federation statistics.
func (m *FederationByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.stitchers, func(s *Stitcher) interface{} {
		return s.Stats()
	})
}
