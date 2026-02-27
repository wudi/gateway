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

// routeFeatureMgr is satisfied by byroute.Factory, byroute.NamedFactory,
// and custom ByRoute types whose AddRoute returns error.
type routeFeatureMgr[C any] interface {
	AddRoute(string, C) error
	RouteIDs() []string
}

// statsFor returns a stats function if the manager has Stats() map[string]any
// and adminPath is non-empty; otherwise returns nil.
func statsFor[C any, M routeFeatureMgr[C]](adminPath string, mgr M) func() any {
	type hasStats interface{ Stats() map[string]any }
	if adminPath != "" {
		if sp, ok := any(mgr).(hasStats); ok {
			return func() any { return sp.Stats() }
		}
	}
	return nil
}

// featureFor creates a Feature backed by a per-route manager.
// getCfg extracts the feature config and whether the feature is active from the route config.
// The manager's AddRoute and RouteIDs are wired automatically; Stats is used when available.
func featureFor[C any, M routeFeatureMgr[C]](name, adminPath string, mgr M, getCfg func(config.RouteConfig) (C, bool)) Feature {
	return newFeature(name, adminPath, func(id string, rc config.RouteConfig) error {
		if cfg, active := getCfg(rc); active {
			return mgr.AddRoute(id, cfg)
		}
		return nil
	}, mgr.RouteIDs, statsFor(adminPath, mgr))
}

// noOpStatsFeature creates a no-op Feature with admin stats from a manager that
// provides RouteIDs() and Stats(). This covers the common case of features whose
// setup is handled outside the feature loop but still need admin API stats.
func noOpStatsFeature[T any, M interface {
	RouteIDs() []string
	Stats() T
}](name, adminPath string, mgr M) Feature {
	return noOpFeature(name, adminPath, mgr.RouteIDs, func() any { return mgr.Stats() })
}

// featureForWithStats is like featureFor but takes an explicit stats function.
// Use this for managers whose Stats() has a typed return value that doesn't match
// the map[string]any signature expected by statsFor.
func featureForWithStats[C any, M routeFeatureMgr[C]](name, adminPath string, mgr M, getCfg func(config.RouteConfig) (C, bool), stats func() any) Feature {
	return newFeature(name, adminPath, func(id string, rc config.RouteConfig) error {
		if cfg, active := getCfg(rc); active {
			return mgr.AddRoute(id, cfg)
		}
		return nil
	}, mgr.RouteIDs, stats)
}

// mergeFeature creates a Feature that merges per-route config with global defaults.
// When the route config is active, it is merged with the global config via merge.
// When only the global config is active, the global config is used as-is.
func mergeFeature[C any, M routeFeatureMgr[C]](name, adminPath string, mgr M,
	getRoute func(config.RouteConfig) (C, bool),
	getGlobal func() (C, bool),
	merge func(C, C) C,
) Feature {
	return newFeature(name, adminPath, func(id string, rc config.RouteConfig) error {
		routeCfg, routeActive := getRoute(rc)
		globalCfg, globalActive := getGlobal()
		if routeActive {
			return mgr.AddRoute(id, merge(routeCfg, globalCfg))
		}
		if globalActive {
			return mgr.AddRoute(id, globalCfg)
		}
		return nil
	}, mgr.RouteIDs, statsFor(adminPath, mgr))
}

// Enableable is implemented by config types with an Enabled field.
// It enables the use of [enabledFeature] and [enabledMerge] which provide
// a simpler API by checking IsEnabled() automatically instead of requiring
// callers to return a (config, active) tuple.
type Enableable interface {
	IsEnabled() bool
}

// enabledFeature creates a Feature backed by a per-route manager where the
// config type implements Enableable. This eliminates the verbose
// func(rc) (cfg, cfg.Enabled) tuple that most featureFor calls require.
func enabledFeature[C Enableable, M routeFeatureMgr[C]](name, adminPath string, mgr M, get func(config.RouteConfig) C) Feature {
	return featureFor(name, adminPath, mgr, func(rc config.RouteConfig) (C, bool) {
		c := get(rc)
		return c, c.IsEnabled()
	})
}

// enabledMerge creates a merge Feature for Enableable config types.
// It reduces the verbose pair of (config, bool) getters to simple value getters.
func enabledMerge[C Enableable, M routeFeatureMgr[C]](name, adminPath string, mgr M,
	getRoute func(config.RouteConfig) C,
	getGlobal func() C,
	merge func(C, C) C,
) Feature {
	return mergeFeature(name, adminPath, mgr,
		func(rc config.RouteConfig) (C, bool) { c := getRoute(rc); return c, c.IsEnabled() },
		func() (C, bool) { c := getGlobal(); return c, c.IsEnabled() },
		merge)
}
