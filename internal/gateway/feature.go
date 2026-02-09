package gateway

import (
	"github.com/example/gateway/internal/cache"
	"github.com/example/gateway/internal/canary"
	"github.com/example/gateway/internal/circuitbreaker"
	"github.com/example/gateway/internal/coalesce"
	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/graphql"
	"github.com/example/gateway/internal/middleware/compression"
	"github.com/example/gateway/internal/middleware/cors"
	"github.com/example/gateway/internal/middleware/accesslog"
	"github.com/example/gateway/internal/middleware/extauth"
	"github.com/example/gateway/internal/middleware/ipfilter"
	"github.com/example/gateway/internal/middleware/errorpages"
	"github.com/example/gateway/internal/middleware/openapi"
	"github.com/example/gateway/internal/middleware/timeout"
	"github.com/example/gateway/internal/middleware/validation"
	"github.com/example/gateway/internal/middleware/versioning"
	"github.com/example/gateway/internal/middleware/waf"
	"github.com/example/gateway/internal/mirror"
	"github.com/example/gateway/internal/rules"
	"github.com/example/gateway/internal/trafficshape"
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
		return f.m.AddRoute(routeID, cfg.CORS)
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
		return f.m.AddRoute(routeID, cfg.Mirror)
	}
	return nil
}
func (f *mirrorFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *mirrorFeature) AdminStats() any     { return f.m.Stats() }

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

// throttleFeature wraps ThrottleByRoute.
type throttleFeature struct {
	m      *trafficshape.ThrottleByRoute
	global *config.ThrottleConfig
}

func (f *throttleFeature) Name() string { return "throttle" }
func (f *throttleFeature) Setup(routeID string, cfg config.RouteConfig) error {
	tc := cfg.TrafficShaping.Throttle
	if tc.Enabled {
		merged := trafficshape.MergeThrottleConfig(tc, *f.global)
		f.m.AddRoute(routeID, merged)
	} else if f.global.Enabled {
		f.m.AddRoute(routeID, *f.global)
	}
	return nil
}
func (f *throttleFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *throttleFeature) AdminStats() any     { return f.m.Stats() }

// bandwidthFeature wraps BandwidthByRoute.
type bandwidthFeature struct {
	m      *trafficshape.BandwidthByRoute
	global *config.BandwidthConfig
}

func (f *bandwidthFeature) Name() string { return "bandwidth" }
func (f *bandwidthFeature) Setup(routeID string, cfg config.RouteConfig) error {
	bc := cfg.TrafficShaping.Bandwidth
	if bc.Enabled {
		merged := trafficshape.MergeBandwidthConfig(bc, *f.global)
		f.m.AddRoute(routeID, merged)
	} else if f.global.Enabled {
		f.m.AddRoute(routeID, *f.global)
	}
	return nil
}
func (f *bandwidthFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *bandwidthFeature) AdminStats() any     { return f.m.Stats() }

// priorityFeature wraps PriorityByRoute.
type priorityFeature struct {
	m      *trafficshape.PriorityByRoute
	global *config.PriorityConfig
}

func (f *priorityFeature) Name() string { return "priority" }
func (f *priorityFeature) Setup(routeID string, cfg config.RouteConfig) error {
	pc := cfg.TrafficShaping.Priority
	if pc.Enabled {
		merged := trafficshape.MergePriorityConfig(pc, *f.global)
		f.m.AddRoute(routeID, merged)
	} else if f.global.Enabled {
		f.m.AddRoute(routeID, *f.global)
	}
	return nil
}
func (f *priorityFeature) RouteIDs() []string { return f.m.RouteIDs() }

// faultInjectionFeature wraps FaultInjectionByRoute.
type faultInjectionFeature struct {
	m      *trafficshape.FaultInjectionByRoute
	global *config.FaultInjectionConfig
}

func (f *faultInjectionFeature) Name() string { return "fault_injection" }
func (f *faultInjectionFeature) Setup(routeID string, cfg config.RouteConfig) error {
	fi := cfg.TrafficShaping.FaultInjection
	if fi.Enabled {
		merged := trafficshape.MergeFaultInjectionConfig(fi, *f.global)
		f.m.AddRoute(routeID, merged)
	} else if f.global.Enabled {
		f.m.AddRoute(routeID, *f.global)
	}
	return nil
}
func (f *faultInjectionFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *faultInjectionFeature) AdminStats() any     { return f.m.Stats() }

// wafFeature wraps WAFByRoute.
type wafFeature struct{ m *waf.WAFByRoute }

func (f *wafFeature) Name() string { return "waf" }
func (f *wafFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.WAF.Enabled {
		return f.m.AddRoute(routeID, cfg.WAF)
	}
	return nil
}
func (f *wafFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *wafFeature) AdminStats() any     { return f.m.Stats() }

// graphqlFeature wraps GraphQLByRoute.
type graphqlFeature struct{ m *graphql.GraphQLByRoute }

func (f *graphqlFeature) Name() string { return "graphql" }
func (f *graphqlFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.GraphQL.Enabled {
		return f.m.AddRoute(routeID, cfg.GraphQL)
	}
	return nil
}
func (f *graphqlFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *graphqlFeature) AdminStats() any     { return f.m.Stats() }

// coalesceFeature wraps CoalesceByRoute.
type coalesceFeature struct{ m *coalesce.CoalesceByRoute }

func (f *coalesceFeature) Name() string { return "coalesce" }
func (f *coalesceFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.Coalesce.Enabled {
		f.m.AddRoute(routeID, cfg.Coalesce)
	}
	return nil
}
func (f *coalesceFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *coalesceFeature) AdminStats() any     { return f.m.Stats() }

// adaptiveConcurrencyFeature wraps AdaptiveConcurrencyByRoute.
type adaptiveConcurrencyFeature struct {
	m      *trafficshape.AdaptiveConcurrencyByRoute
	global *config.AdaptiveConcurrencyConfig
}

func (f *adaptiveConcurrencyFeature) Name() string { return "adaptive_concurrency" }
func (f *adaptiveConcurrencyFeature) Setup(routeID string, cfg config.RouteConfig) error {
	ac := cfg.TrafficShaping.AdaptiveConcurrency
	if ac.Enabled {
		merged := trafficshape.MergeAdaptiveConcurrencyConfig(ac, *f.global)
		f.m.AddRoute(routeID, merged)
	} else if f.global.Enabled {
		f.m.AddRoute(routeID, *f.global)
	}
	return nil
}
func (f *adaptiveConcurrencyFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *adaptiveConcurrencyFeature) AdminStats() any     { return f.m.Stats() }

// canaryFeature wraps CanaryByRoute.
// Setup is a no-op because canary needs the WeightedBalancer reference which is
// only available after the route proxy is created. Actual setup happens in addRoute().
type canaryFeature struct{ m *canary.CanaryByRoute }

func (f *canaryFeature) Name() string                                    { return "canary" }
func (f *canaryFeature) Setup(routeID string, cfg config.RouteConfig) error { return nil }
func (f *canaryFeature) RouteIDs() []string                              { return f.m.RouteIDs() }
func (f *canaryFeature) AdminStats() any                                 { return f.m.Stats() }

// extAuthFeature wraps ExtAuthByRoute.
type extAuthFeature struct{ m *extauth.ExtAuthByRoute }

func (f *extAuthFeature) Name() string { return "ext_auth" }
func (f *extAuthFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.ExtAuth.Enabled {
		return f.m.AddRoute(routeID, cfg.ExtAuth)
	}
	return nil
}
func (f *extAuthFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *extAuthFeature) AdminStats() any    { return f.m.Stats() }

// versioningFeature wraps VersioningByRoute.
type versioningFeature struct{ m *versioning.VersioningByRoute }

func (f *versioningFeature) Name() string { return "versioning" }
func (f *versioningFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.Versioning.Enabled {
		return f.m.AddRoute(routeID, cfg.Versioning)
	}
	return nil
}
func (f *versioningFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *versioningFeature) AdminStats() any     { return f.m.Stats() }

// accessLogFeature wraps AccessLogByRoute.
type accessLogFeature struct{ m *accesslog.AccessLogByRoute }

func (f *accessLogFeature) Name() string { return "access_log" }
func (f *accessLogFeature) Setup(routeID string, cfg config.RouteConfig) error {
	al := cfg.AccessLog
	if al.Enabled != nil || al.Format != "" ||
		len(al.HeadersInclude) > 0 || len(al.HeadersExclude) > 0 ||
		al.Body.Enabled ||
		al.Conditions.SampleRate > 0 || len(al.Conditions.StatusCodes) > 0 ||
		len(al.Conditions.Methods) > 0 {
		return f.m.AddRoute(routeID, al)
	}
	return nil
}
func (f *accessLogFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *accessLogFeature) AdminStats() any     { return f.m.Stats() }

// openapiFeature wraps OpenAPIByRoute.
type openapiFeature struct{ m *openapi.OpenAPIByRoute }

func (f *openapiFeature) Name() string { return "openapi" }
func (f *openapiFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.OpenAPI.SpecFile != "" || cfg.OpenAPI.SpecID != "" {
		return f.m.AddRoute(routeID, cfg.OpenAPI)
	}
	return nil
}
func (f *openapiFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *openapiFeature) AdminStats() any     { return f.m.Stats() }

// timeoutFeature wraps TimeoutByRoute.
type timeoutFeature struct{ m *timeout.TimeoutByRoute }

func (f *timeoutFeature) Name() string { return "timeout" }
func (f *timeoutFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if cfg.TimeoutPolicy.IsActive() {
		f.m.AddRoute(routeID, cfg.TimeoutPolicy)
	}
	return nil
}
func (f *timeoutFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *timeoutFeature) AdminStats() any     { return f.m.Stats() }

// errorPagesFeature wraps ErrorPagesByRoute.
type errorPagesFeature struct {
	m      *errorpages.ErrorPagesByRoute
	global *config.ErrorPagesConfig
}

func (f *errorPagesFeature) Name() string { return "error_pages" }
func (f *errorPagesFeature) Setup(routeID string, cfg config.RouteConfig) error {
	if f.global.IsActive() || cfg.ErrorPages.IsActive() {
		return f.m.AddRoute(routeID, *f.global, cfg.ErrorPages)
	}
	return nil
}
func (f *errorPagesFeature) RouteIDs() []string { return f.m.RouteIDs() }
func (f *errorPagesFeature) AdminStats() any     { return f.m.Stats() }
