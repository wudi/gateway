package gateway

// Feature is a per-route capability that can be set up generically.
type Feature interface {
	Name() string
	Setup(routeID string, cfg RouteConfig) error
	RouteIDs() []string
}

// AdminStatsProvider is an optional interface for features that expose admin stats.
type AdminStatsProvider interface {
	AdminStats() any
	AdminPath() string
}

// MiddlewareProvider is an optional interface for features that contribute
// per-route middleware slots to the chain.
type MiddlewareProvider interface {
	MiddlewareSlots() []MiddlewareSlot
}

// GlobalMiddlewareProvider is an optional interface for features that contribute
// global middleware slots to the handler chain.
type GlobalMiddlewareProvider interface {
	GlobalMiddlewareSlots() []GlobalMiddlewareSlot
}

// ConfigValidator is an optional interface for features that want to validate
// their extension config at Build() time (before any routes are added).
type ConfigValidator interface {
	ValidateConfig(cfg *Config) error
}

// Reconfigurable is an optional interface for features that support hot reload.
type Reconfigurable interface {
	Reconfigure(cfg *Config) error
}
