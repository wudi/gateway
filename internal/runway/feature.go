package runway

import "github.com/wudi/runway/config"

// Feature is a per-route capability that can be set up generically.
type Feature interface {
	Name() string
	Setup(routeID string, cfg config.RouteConfig) error
	RouteIDs() []string
}

// AdminStatsProvider is an optional interface for features that expose admin stats.
// AdminPath returns the HTTP path for the admin endpoint (e.g. "/mock-responses").
// An empty path means the feature has no dedicated admin endpoint.
type AdminStatsProvider interface {
	AdminStats() any
	AdminPath() string
}

// featureFunc implements Feature via closures.
type featureFunc struct {
	name      string
	adminPath string
	setup     func(routeID string, cfg config.RouteConfig) error
	routeIDs  func() []string
}

func (f *featureFunc) Name() string                                       { return f.name }
func (f *featureFunc) Setup(routeID string, cfg config.RouteConfig) error { return f.setup(routeID, cfg) }
func (f *featureFunc) RouteIDs() []string                                 { return f.routeIDs() }

// featureFuncStats extends featureFunc with AdminStatsProvider.
type featureFuncStats struct {
	featureFunc
	stats func() any
}

func (f *featureFuncStats) AdminStats() any  { return f.stats() }
func (f *featureFuncStats) AdminPath() string { return f.adminPath }

// newFeature creates a Feature from closures. If stats is non-nil, the feature
// also implements AdminStatsProvider. adminPath is the HTTP path for the admin
// endpoint (e.g. "/mock-responses"); empty means no admin endpoint.
func newFeature(name, adminPath string, setup func(routeID string, cfg config.RouteConfig) error, routeIDs func() []string, stats func() any) Feature {
	ff := featureFunc{name: name, adminPath: adminPath, setup: setup, routeIDs: routeIDs}
	if stats != nil {
		return &featureFuncStats{featureFunc: ff, stats: stats}
	}
	return &ff
}

// noOpFeature creates a Feature whose Setup is a no-op.
func noOpFeature(name, adminPath string, routeIDs func() []string, stats func() any) Feature {
	return newFeature(name, adminPath, func(string, config.RouteConfig) error { return nil }, routeIDs, stats)
}
