package runway

import (
	"context"
	"fmt"
	"net/http"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/health"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/logging"
	"github.com/wudi/runway/internal/middleware/ratelimit"
	"github.com/wudi/runway/internal/middleware/sse"
	"github.com/wudi/runway/internal/proxy"
	"github.com/wudi/runway/internal/registry"
	"github.com/wudi/runway/internal/router"
	"go.uber.org/zap"
)

// routeSetup holds parameters that differ between initial route setup
// (addRoute) and config reload (addRouteForState). This lets setupRoute
// contain the shared logic exactly once.
type routeSetup struct {
	cfg             *config.Config
	rtr             *router.Router
	rm              *routeManagers
	features        []Feature
	registerBackend func(health.Backend)
	watchService    func(routeID, serviceName string, tags []string)
	storeProxy      func(routeID string, rp *proxy.RouteProxy)
	buildHandler    func(routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler
	storeHandler    func(routeID string, h http.Handler)
}

// setupRoute adds a single route using the shared logic. It is called by both
// addRoute (initial startup) and addRouteForState (config reload).
func (g *Runway) setupRoute(rs *routeSetup, routeCfg config.RouteConfig) error {
	routeCfg = resolveUpstreamRefs(rs.cfg, routeCfg)

	if err := rs.rtr.AddRoute(routeCfg); err != nil {
		return err
	}

	route := rs.rtr.GetRoute(routeCfg.ID)
	if route == nil {
		return fmt.Errorf("route not found after adding: %s", routeCfg.ID)
	}
	route.UpstreamName = routeCfg.Upstream

	// Set up backends (skip for echo, sequential, aggregate, AI routes — no backend needed)
	var routeProxy *proxy.RouteProxy
	if !routeCfg.Echo && !routeCfg.Sequential.Enabled && !routeCfg.Aggregate.Enabled && !routeCfg.AI.Enabled {
		var backends []*loadbalancer.Backend

		if routeCfg.Service.Name != "" {
			ctx := context.Background()
			services, err := g.registry.DiscoverWithTags(ctx, routeCfg.Service.Name, routeCfg.Service.Tags)
			if err != nil {
				logging.Warn("Failed to discover service",
					zap.String("service", routeCfg.Service.Name),
					zap.Error(err),
				)
			}
			for _, svc := range services {
				b := &loadbalancer.Backend{
					URL:     svc.URL(),
					Weight:  1,
					Healthy: svc.Health == registry.HealthPassing,
				}
				b.InitParsedURL()
				backends = append(backends, b)
			}
			rs.watchService(routeCfg.ID, routeCfg.Service.Name, routeCfg.Service.Tags)
		} else {
			usHC := upstreamHCConfig(rs.cfg, routeCfg.Upstream)
			backends = buildBackends(routeCfg.Backends, rs.registerBackend, rs.cfg.HealthCheck, usHC)
		}

		// Create route proxy with the appropriate balancer
		if routeCfg.Versioning.Enabled {
			versionBackends := make(map[string][]*loadbalancer.Backend)
			for ver, vcfg := range routeCfg.Versioning.Versions {
				verUSHC := upstreamHCConfig(rs.cfg, vcfg.Upstream)
				versionBackends[ver] = buildBackends(vcfg.Backends, rs.registerBackend, rs.cfg.HealthCheck, verUSHC)
			}
			vb := loadbalancer.NewVersionedBalancer(versionBackends, routeCfg.Versioning.DefaultVersion)
			routeProxy = proxy.NewRouteProxyWithBalancer(g.proxy, route, vb)
		} else if len(routeCfg.TrafficSplit) > 0 {
			var wb *loadbalancer.WeightedBalancer
			if routeCfg.Sticky.Enabled {
				wb = loadbalancer.NewWeightedBalancerWithSticky(routeCfg.TrafficSplit, routeCfg.Sticky)
			} else {
				wb = loadbalancer.NewWeightedBalancer(routeCfg.TrafficSplit)
			}
			routeProxy = proxy.NewRouteProxyWithBalancer(g.proxy, route, wb)
		} else {
			bal := createBalancerForBackends(routeCfg, backends)
			if len(routeCfg.TenantBackends) > 0 {
				tenantBals := make(map[string]loadbalancer.Balancer, len(routeCfg.TenantBackends))
				for tid, tBackends := range routeCfg.TenantBackends {
					tBacks := buildBackends(tBackends, rs.registerBackend, rs.cfg.HealthCheck, nil)
					tenantBals[tid] = createBalancerForBackends(routeCfg, tBacks)
				}
				bal = loadbalancer.NewTenantAwareBalancer(bal, tenantBals)
			}
			if routeCfg.SessionAffinity.Enabled {
				bal = loadbalancer.NewSessionAffinityBalancer(bal, routeCfg.SessionAffinity)
			}
			routeProxy = proxy.NewRouteProxyWithBalancer(g.proxy, route, bal)
		}
		rs.storeProxy(routeCfg.ID, routeProxy)

		// Wire shared retry budget pool
		if routeCfg.RetryPolicy.BudgetPool != "" {
			if pool, ok := rs.rm.budgetPools[routeCfg.RetryPolicy.BudgetPool]; ok {
				routeProxy.SetRetryBudget(pool)
			}
		}
	}

	// Rate limiting (unique setup signature, not in feature loop)
	if len(routeCfg.RateLimit.Tiers) > 0 {
		tiers := make(map[string]ratelimit.Config, len(routeCfg.RateLimit.Tiers))
		for name, tc := range routeCfg.RateLimit.Tiers {
			tiers[name] = ratelimit.Config{
				Rate:   tc.Rate,
				Period: tc.Period,
				Burst:  tc.Burst,
			}
		}
		var keyFn func(*http.Request) string
		if routeCfg.RateLimit.Key != "" {
			keyFn = ratelimit.BuildKeyFunc(false, routeCfg.RateLimit.Key)
		}
		rs.rm.rateLimiters.AddRouteTiered(routeCfg.ID, ratelimit.TieredConfig{
			Tiers:       tiers,
			TierKey:     routeCfg.RateLimit.TierKey,
			DefaultTier: routeCfg.RateLimit.DefaultTier,
			KeyFn:       keyFn,
		})
	} else if routeCfg.RateLimit.Enabled || routeCfg.RateLimit.Rate > 0 {
		if routeCfg.RateLimit.Mode == "distributed" && g.redisClient != nil {
			rs.rm.rateLimiters.AddRouteDistributed(routeCfg.ID, ratelimit.RedisLimiterConfig{
				Client: g.redisClient,
				Prefix: "gw:rl:" + routeCfg.ID + ":",
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		} else if routeCfg.RateLimit.Algorithm == "sliding_window" {
			rs.rm.rateLimiters.AddRouteSlidingWindow(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		} else {
			rs.rm.rateLimiters.AddRoute(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
				Key:    routeCfg.RateLimit.Key,
			})
		}
	}

	// gRPC handler
	if routeCfg.GRPC.Enabled {
		rs.rm.grpcHandlers.AddRoute(routeCfg.ID, routeCfg.GRPC)
	}

	// gRPC reflection proxy
	if routeCfg.GRPC.Enabled && routeCfg.GRPC.Reflection.Enabled {
		var grpcBackends []string
		for _, b := range routeCfg.Backends {
			grpcBackends = append(grpcBackends, b.URL)
		}
		if len(grpcBackends) > 0 {
			rs.rm.grpcReflection.AddRoute(routeCfg.ID, grpcBackends, routeCfg.GRPC.Reflection)
		}
	}

	// GraphQL federation
	if routeCfg.GraphQLFederation.Enabled {
		if err := rs.rm.federationHandlers.AddRoute(routeCfg.ID, routeCfg.GraphQLFederation, nil); err != nil {
			return fmt.Errorf("graphql federation: route %s: %w", routeCfg.ID, err)
		}
	}

	// Protocol translator
	if routeCfg.Protocol.Type != "" && routeProxy != nil {
		bal := routeProxy.GetBalancer()
		if err := rs.rm.translators.AddRoute(routeCfg.ID, routeCfg.Protocol, bal); err != nil {
			return fmt.Errorf("protocol translator: route %s: %w", routeCfg.ID, err)
		}
	}

	// Generic features
	for _, f := range rs.features {
		if err := f.Setup(routeCfg.ID, routeCfg); err != nil {
			return fmt.Errorf("feature %s: route %s: %w", f.Name(), routeCfg.ID, err)
		}
	}

	// External features (from public builder)
	for _, ef := range g.externalFeatures {
		if err := ef.Feature.Setup(routeCfg.ID, routeCfg); err != nil {
			return fmt.Errorf("external feature %s: route %s: %w", ef.Feature.Name(), routeCfg.ID, err)
		}
	}

	// Sequential handler (needs transport from proxy pool)
	if routeCfg.Sequential.Enabled {
		transport := g.proxy.GetTransportPool().Get(routeCfg.Upstream)
		ch := routeCfg.CompletionHeader || rs.cfg.CompletionHeader
		if err := rs.rm.sequentialHandlers.AddRoute(routeCfg.ID, routeCfg.Sequential, transport, ch); err != nil {
			return fmt.Errorf("sequential: route %s: %w", routeCfg.ID, err)
		}
	}

	// Aggregate handler (needs transport from proxy pool)
	if routeCfg.Aggregate.Enabled {
		transport := g.proxy.GetTransportPool().Get(routeCfg.Upstream)
		ch := routeCfg.CompletionHeader || rs.cfg.CompletionHeader
		if err := rs.rm.aggregateHandlers.AddRoute(routeCfg.ID, routeCfg.Aggregate, transport, ch); err != nil {
			return fmt.Errorf("aggregate: route %s: %w", routeCfg.ID, err)
		}
	}

	// Lambda backend handler
	if routeCfg.Lambda.Enabled {
		if err := rs.rm.lambdaHandlers.AddRoute(routeCfg.ID, routeCfg.Lambda); err != nil {
			return fmt.Errorf("lambda: route %s: %w", routeCfg.ID, err)
		}
	}

	// AMQP backend handler
	if routeCfg.AMQP.Enabled {
		if err := rs.rm.amqpHandlers.AddRoute(routeCfg.ID, routeCfg.AMQP); err != nil {
			return fmt.Errorf("amqp: route %s: %w", routeCfg.ID, err)
		}
	}

	// PubSub backend handler
	if routeCfg.PubSub.Enabled {
		if err := rs.rm.pubsubHandlers.AddRoute(routeCfg.ID, routeCfg.PubSub); err != nil {
			return fmt.Errorf("pubsub: route %s: %w", routeCfg.ID, err)
		}
	}

	// Override per-try timeout with backend timeout
	if routeCfg.TimeoutPolicy.Backend > 0 && routeProxy != nil {
		routeProxy.SetPerTryTimeout(routeCfg.TimeoutPolicy.Backend)
	}

	// Canary setup (needs WeightedBalancer)
	if routeCfg.Canary.Enabled && routeProxy != nil {
		if wb, ok := routeProxy.GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			if err := rs.rm.canaryControllers.AddRoute(routeCfg.ID, routeCfg.Canary, wb); err != nil {
				return fmt.Errorf("canary: route %s: %w", routeCfg.ID, err)
			}
			if routeCfg.Canary.AutoStart {
				if ctrl := rs.rm.canaryControllers.Lookup(routeCfg.ID); ctrl != nil {
					if err := ctrl.Start(); err != nil {
						return fmt.Errorf("canary auto-start: route %s: %w", routeCfg.ID, err)
					}
				}
			}
		}
	}

	// Blue-green setup (needs WeightedBalancer)
	if routeCfg.BlueGreen.Enabled && routeProxy != nil {
		if wb, ok := routeProxy.GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			rs.rm.blueGreenControllers.AddRoute(routeCfg.ID, routeCfg.BlueGreen, wb, g.healthChecker)
		}
	}

	// A/B test setup (needs WeightedBalancer)
	if routeCfg.ABTest.Enabled && routeProxy != nil {
		if wb, ok := routeProxy.GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			rs.rm.abTests.AddRoute(routeCfg.ID, routeCfg.ABTest, wb)
		}
	}

	// Outlier detection (needs Balancer)
	if routeCfg.OutlierDetection.Enabled && routeProxy != nil {
		rs.rm.outlierDetectors.AddRoute(routeCfg.ID, routeCfg.OutlierDetection, routeProxy.GetBalancer())
	}

	// Backend backpressure (needs Balancer)
	if routeCfg.Backpressure.Enabled && routeProxy != nil {
		rs.rm.backpressureHandlers.AddRoute(routeCfg.ID, routeCfg.Backpressure, routeProxy.GetBalancer())
	}

	// SSE fan-out hub (needs balancer from routeProxy)
	if routeCfg.SSE.Enabled && routeCfg.SSE.Fanout.Enabled && routeProxy != nil {
		hub := sse.NewHub(routeCfg.SSE.Fanout, routeProxy.GetBalancer())
		if sh := rs.rm.sseHandlers.Lookup(routeCfg.ID); sh != nil {
			sh.SetHub(hub)
			hub.Start()
		}
	}

	// Build the per-route middleware pipeline handler
	handler := rs.buildHandler(routeCfg.ID, routeCfg, route, routeProxy)
	rs.storeHandler(routeCfg.ID, handler)

	return nil
}

// buildBackends constructs loadbalancer Backends from config and registers
// each with the health checker. This eliminates the repeated weight-defaulting
// and InitParsedURL loop that appeared in three places (standard, versioned,
// and tenant backends).
func buildBackends(cfgBackends []config.BackendConfig, register func(health.Backend), globalHC config.HealthCheckConfig, upstreamHC *config.HealthCheckConfig) []*loadbalancer.Backend {
	backends := make([]*loadbalancer.Backend, 0, len(cfgBackends))
	for _, b := range cfgBackends {
		weight := b.Weight
		if weight == 0 {
			weight = 1
		}
		be := &loadbalancer.Backend{URL: b.URL, Weight: weight, Healthy: true}
		be.InitParsedURL()
		backends = append(backends, be)
		register(upstreamHealthCheck(b.URL, globalHC, upstreamHC, b.HealthCheck))
	}
	return backends
}

// upstreamHCConfig returns the health check config for an upstream, or nil.
func upstreamHCConfig(cfg *config.Config, upstreamName string) *config.HealthCheckConfig {
	if upstreamName == "" {
		return nil
	}
	if us, ok := cfg.Upstreams[upstreamName]; ok {
		return us.HealthCheck
	}
	return nil
}
