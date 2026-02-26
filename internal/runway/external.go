package runway

import (
	"net/http"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// namedSlot is a middleware slot with a name (for anchor-based insertion of custom middleware).
type namedSlot struct {
	name  string
	build func() middleware.Middleware
}

// ExternalOptions carries configuration from the public gateway.RunwayBuilder
// into the internal gateway.
type ExternalOptions struct {
	UseDefaults      bool
	CustomSlots      []CustomSlot
	CustomGlobal     []CustomGlobalSlot
	ExternalFeatures []ExternalFeature
}

// CustomSlot is the internal representation of a custom per-route middleware.
type CustomSlot struct {
	Name   string
	After  string
	Before string
	Build  func(routeID string, cfg config.RouteConfig) func(http.Handler) http.Handler
}

// CustomGlobalSlot is the internal representation of a custom global middleware.
type CustomGlobalSlot struct {
	Name   string
	After  string
	Before string
	Build  func(cfg *config.Config) func(http.Handler) http.Handler
}

// ExternalFeature wraps a public Feature interface for use in the internal gateway.
type ExternalFeature struct {
	Feature interface {
		Name() string
		Setup(routeID string, cfg config.RouteConfig) error
		RouteIDs() []string
	}
}

// ExternalAdminStatsProvider is tested via type assertion on ExternalFeature.Feature.
type ExternalAdminStatsProvider interface {
	AdminStats() any
	AdminPath() string
}

// ExternalReconfigurable is tested via type assertion for hot reload support.
type ExternalReconfigurable interface {
	Reconfigure(cfg *config.Config) error
}
