package gateway

import (
	"github.com/example/gateway/internal/cache"
	"github.com/example/gateway/internal/circuitbreaker"
	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/middleware/compression"
	"github.com/example/gateway/internal/middleware/cors"
	"github.com/example/gateway/internal/middleware/ipfilter"
	"github.com/example/gateway/internal/middleware/validation"
	"github.com/example/gateway/internal/mirror"
	"github.com/example/gateway/internal/rules"
)

// Feature is a per-route capability that can be set up generically.
type Feature interface {
	Name() string
	Setup(routeID string, cfg config.RouteConfig) error
	RouteIDs() []string
}

// AdminStatsProvider is an optional interface for features that expose admin stats.
type AdminStatsProvider interface {
	AdminStats() any
}

// --- Feature adapters ---

// ipFilterFeature wraps IPFilterByRoute.
type ipFilterFeature struct{ m *ipfilter.IPFilterByRoute }

func (f *ipFilterFeature) Name() string { return "ip_filter" }
func (f *ipFilterFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.IPFilter.Enabled {
		return f.m.AddRoute(routeID, cfg.IPFilter)
	}
	return nil
}
func (f *ipFilterFeature) RouteIDs() []string { return f.m.RouteIDs() }

// corsFeature wraps CORSByRoute.
type corsFeature struct{ m *cors.CORSByRoute }

func (f *corsFeature) Name() string { return "cors" }
func (f *corsFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.CORS.Enabled {
		f.m.AddRoute(routeID, cfg.CORS)
	}
	return nil
}
func (f *corsFeature) RouteIDs() []string { return f.m.RouteIDs() }

// circuitBreakerFeature wraps BreakerByRoute.
type circuitBreakerFeature struct{ m *circuitbreaker.BreakerByRoute }

func (f *circuitBreakerFeature) Name() string { return "circuit_breaker" }
func (f *circuitBreakerFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.CircuitBreaker.Enabled {
		f.m.AddRoute(routeID, cfg.CircuitBreaker)
	}
	return nil
}
func (f *circuitBreakerFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *circuitBreakerFeature) AdminStats() any     { return f.m.Snapshots() }

// cacheFeature wraps CacheByRoute.
type cacheFeature struct{ m *cache.CacheByRoute }

func (f *cacheFeature) Name() string { return "cache" }
func (f *cacheFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.Cache.Enabled {
		f.m.AddRoute(routeID, cfg.Cache)
	}
	return nil
}
func (f *cacheFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *cacheFeature) AdminStats() any     { return f.m.Stats() }

// compressionFeature wraps CompressorByRoute.
type compressionFeature struct{ m *compression.CompressorByRoute }

func (f *compressionFeature) Name() string { return "compression" }
func (f *compressionFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.Compression.Enabled {
		f.m.AddRoute(routeID, cfg.Compression)
	}
	return nil
}
func (f *compressionFeature) RouteIDs() []string { return f.m.RouteIDs() }

// validationFeature wraps ValidatorByRoute.
type validationFeature struct{ m *validation.ValidatorByRoute }

func (f *validationFeature) Name() string { return "validation" }
func (f *validationFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.Validation.Enabled {
		return f.m.AddRoute(routeID, cfg.Validation)
	}
	return nil
}
func (f *validationFeature) RouteIDs() []string { return f.m.RouteIDs() }

// mirrorFeature wraps MirrorByRoute.
type mirrorFeature struct{ m *mirror.MirrorByRoute }

func (f *mirrorFeature) Name() string { return "mirror" }
func (f *mirrorFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.Mirror.Enabled {
		f.m.AddRoute(routeID, cfg.Mirror)
	}
	return nil
}
func (f *mirrorFeature) RouteIDs() []string { return f.m.RouteIDs() }

// rulesFeature wraps RulesByRoute.
type rulesFeature struct{ m *rules.RulesByRoute }

func (f *rulesFeature) Name() string { return "rules" }
func (f *rulesFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if len(cfg.Rules.Request) > 0 || len(cfg.Rules.Response) > 0 {
		return f.m.AddRoute(routeID, cfg.Rules)
	}
	return nil
}
func (f *rulesFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *rulesFeature) AdminStats() any     { return f.m.Stats() }
