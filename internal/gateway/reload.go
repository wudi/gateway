package gateway

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/wudi/gateway/internal/cache"
	"github.com/wudi/gateway/internal/canary"
	"github.com/wudi/gateway/internal/circuitbreaker"
	"github.com/wudi/gateway/internal/coalesce"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/graphql"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/loadbalancer/outlier"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware/accesslog"
	"github.com/wudi/gateway/internal/middleware/auth"
	"github.com/wudi/gateway/internal/middleware/compression"
	"github.com/wudi/gateway/internal/middleware/cors"
	"github.com/wudi/gateway/internal/middleware/csrf"
	"github.com/wudi/gateway/internal/middleware/errorpages"
	"github.com/wudi/gateway/internal/middleware/extauth"
	"github.com/wudi/gateway/internal/middleware/idempotency"
	"github.com/wudi/gateway/internal/middleware/ipfilter"
	"github.com/wudi/gateway/internal/middleware/nonce"
	"github.com/wudi/gateway/internal/middleware/decompress"
	"github.com/wudi/gateway/internal/middleware/securityheaders"
	"github.com/wudi/gateway/internal/middleware/signing"
	openapivalidation "github.com/wudi/gateway/internal/middleware/openapi"
	"github.com/wudi/gateway/internal/middleware/ratelimit"
	"github.com/wudi/gateway/internal/middleware/timeout"
	"github.com/wudi/gateway/internal/middleware/validation"
	"github.com/wudi/gateway/internal/middleware/versioning"
	"github.com/wudi/gateway/internal/middleware/waf"
	"github.com/wudi/gateway/internal/mirror"
	"github.com/wudi/gateway/internal/proxy"
	grpcproxy "github.com/wudi/gateway/internal/proxy/grpc"
	"github.com/wudi/gateway/internal/proxy/protocol"
	"github.com/wudi/gateway/internal/registry"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/internal/rules"
	"github.com/wudi/gateway/internal/trafficshape"
	"github.com/wudi/gateway/internal/variables"
	"github.com/wudi/gateway/internal/webhook"
	"github.com/wudi/gateway/internal/websocket"
	"go.uber.org/zap"
)

// ReloadResult represents the outcome of a config reload.
type ReloadResult struct {
	Success   bool      `json:"success"`
	Timestamp time.Time `json:"timestamp"`
	Error     string    `json:"error,omitempty"`
	Changes   []string  `json:"changes,omitempty"`
}

// gatewayState holds all route-scoped state that gets replaced during a reload.
type gatewayState struct {
	config        *config.Config
	router        *router.Router
	routeProxies  map[string]*proxy.RouteProxy
	routeHandlers map[string]http.Handler
	watchCancels  map[string]context.CancelFunc
	features      []Feature

	// ByRoute managers
	circuitBreakers   *circuitbreaker.BreakerByRoute
	caches            *cache.CacheByRoute
	ipFilters         *ipfilter.IPFilterByRoute
	globalIPFilter    *ipfilter.Filter
	corsHandlers      *cors.CORSByRoute
	compressors       *compression.CompressorByRoute
	validators        *validation.ValidatorByRoute
	mirrors           *mirror.MirrorByRoute
	routeRules        *rules.RulesByRoute
	globalRules       *rules.RuleEngine
	throttlers        *trafficshape.ThrottleByRoute
	bandwidthLimiters *trafficshape.BandwidthByRoute
	priorityAdmitter  *trafficshape.PriorityAdmitter
	priorityConfigs   *trafficshape.PriorityByRoute
	faultInjectors    *trafficshape.FaultInjectionByRoute
	wafHandlers       *waf.WAFByRoute
	graphqlParsers    *graphql.GraphQLByRoute
	coalescers        *coalesce.CoalesceByRoute
	canaryControllers *canary.CanaryByRoute
	adaptiveLimiters  *trafficshape.AdaptiveConcurrencyByRoute
	extAuths          *extauth.ExtAuthByRoute
	versioners        *versioning.VersioningByRoute
	accessLogConfigs  *accesslog.AccessLogByRoute
	openapiValidators *openapivalidation.OpenAPIByRoute
	timeoutConfigs    *timeout.TimeoutByRoute
	errorPages        *errorpages.ErrorPagesByRoute
	nonceCheckers     *nonce.NonceByRoute
	csrfProtectors      *csrf.CSRFByRoute
	idempotencyHandlers *idempotency.IdempotencyByRoute
	backendSigners      *signing.SigningByRoute
	decompressors       *decompress.DecompressorByRoute
	securityHeaders     *securityheaders.SecurityHeadersByRoute
	outlierDetectors    *outlier.DetectorByRoute
	translators       *protocol.TranslatorByRoute
	rateLimiters      *ratelimit.RateLimitByRoute
	grpcHandlers      map[string]*grpcproxy.Handler

	// Auth providers
	apiKeyAuth *auth.APIKeyAuth
	jwtAuth    *auth.JWTAuth
	oauthAuth  *auth.OAuthAuth
}

// buildState builds all route-scoped state from a config.
// Shared infrastructure (proxy, healthChecker, registry, metricsCollector, redisClient, tracer) is
// passed via the Gateway and reused without replacement.
func (g *Gateway) buildState(cfg *config.Config) (*gatewayState, error) {
	s := &gatewayState{
		config:            cfg,
		router:            router.New(),
		routeProxies:      make(map[string]*proxy.RouteProxy),
		routeHandlers:     make(map[string]http.Handler),
		watchCancels:      make(map[string]context.CancelFunc),
		circuitBreakers:   circuitbreaker.NewBreakerByRoute(),
		caches:            cache.NewCacheByRoute(g.redisClient),
		ipFilters:         ipfilter.NewIPFilterByRoute(),
		corsHandlers:      cors.NewCORSByRoute(),
		compressors:       compression.NewCompressorByRoute(),
		validators:        validation.NewValidatorByRoute(),
		mirrors:           mirror.NewMirrorByRoute(),
		routeRules:        rules.NewRulesByRoute(),
		throttlers:        trafficshape.NewThrottleByRoute(),
		bandwidthLimiters: trafficshape.NewBandwidthByRoute(),
		priorityConfigs:   trafficshape.NewPriorityByRoute(),
		faultInjectors:    trafficshape.NewFaultInjectionByRoute(),
		wafHandlers:       waf.NewWAFByRoute(),
		graphqlParsers:    graphql.NewGraphQLByRoute(),
		coalescers:        coalesce.NewCoalesceByRoute(),
		canaryControllers: canary.NewCanaryByRoute(),
		adaptiveLimiters:  trafficshape.NewAdaptiveConcurrencyByRoute(),
		extAuths:          extauth.NewExtAuthByRoute(),
		versioners:        versioning.NewVersioningByRoute(),
		accessLogConfigs:  accesslog.NewAccessLogByRoute(),
		openapiValidators: openapivalidation.NewOpenAPIByRoute(),
		timeoutConfigs:    timeout.NewTimeoutByRoute(),
		errorPages:        errorpages.NewErrorPagesByRoute(),
		nonceCheckers:     nonce.NewNonceByRoute(),
		csrfProtectors:      csrf.NewCSRFByRoute(),
		idempotencyHandlers: idempotency.NewIdempotencyByRoute(),
		backendSigners:      signing.NewSigningByRoute(),
		decompressors:       decompress.NewDecompressorByRoute(),
		securityHeaders:     securityheaders.NewSecurityHeadersByRoute(),
		outlierDetectors:    outlier.NewDetectorByRoute(),
		translators:       protocol.NewTranslatorByRoute(),
		rateLimiters:      ratelimit.NewRateLimitByRoute(),
		grpcHandlers:      make(map[string]*grpcproxy.Handler),
	}

	// Initialize shared priority admitter if global priority is enabled
	if cfg.TrafficShaping.Priority.Enabled {
		s.priorityAdmitter = trafficshape.NewPriorityAdmitter(cfg.TrafficShaping.Priority.MaxConcurrent)
	}

	// Register features
	s.features = []Feature{
		&ipFilterFeature{s.ipFilters},
		&corsFeature{s.corsHandlers},
		&circuitBreakerFeature{s.circuitBreakers},
		&cacheFeature{s.caches},
		&compressionFeature{s.compressors},
		&validationFeature{s.validators},
		&mirrorFeature{s.mirrors},
		&rulesFeature{s.routeRules},
		&throttleFeature{m: s.throttlers, global: &cfg.TrafficShaping.Throttle},
		&bandwidthFeature{m: s.bandwidthLimiters, global: &cfg.TrafficShaping.Bandwidth},
		&priorityFeature{m: s.priorityConfigs, global: &cfg.TrafficShaping.Priority},
		&faultInjectionFeature{m: s.faultInjectors, global: &cfg.TrafficShaping.FaultInjection},
		&adaptiveConcurrencyFeature{m: s.adaptiveLimiters, global: &cfg.TrafficShaping.AdaptiveConcurrency},
		&wafFeature{s.wafHandlers},
		&graphqlFeature{s.graphqlParsers},
		&coalesceFeature{s.coalescers},
		&canaryFeature{s.canaryControllers},
		&extAuthFeature{s.extAuths},
		&versioningFeature{s.versioners},
		&accessLogFeature{s.accessLogConfigs},
		&openapiFeature{s.openapiValidators},
		&timeoutFeature{s.timeoutConfigs},
		&errorPagesFeature{m: s.errorPages, global: &cfg.ErrorPages},
		&nonceFeature{m: s.nonceCheckers, global: &cfg.Nonce, redis: g.redisClient},
		&csrfFeature{m: s.csrfProtectors, global: &cfg.CSRF},
		&idempotencyFeature{m: s.idempotencyHandlers, global: &cfg.Idempotency, redis: g.redisClient},
		&outlierDetectionFeature{s.outlierDetectors},
		&signingFeature{m: s.backendSigners, global: &cfg.BackendSigning},
		&decompressFeature{m: s.decompressors, global: &cfg.RequestDecompression},
		&securityHeadersFeature{m: s.securityHeaders, global: &cfg.SecurityHeaders},
	}

	// Wire webhook callbacks on new state's managers
	if g.webhookDispatcher != nil {
		s.circuitBreakers.SetOnStateChange(func(routeID, from, to string) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.CircuitBreakerStateChange, routeID, map[string]interface{}{
				"from": from, "to": to,
			}))
		})
		s.canaryControllers.SetOnEvent(func(routeID, eventType string, data map[string]interface{}) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.EventType(eventType), routeID, data))
		})
		s.outlierDetectors.SetCallbacks(
			func(routeID, backend, reason string) {
				g.webhookDispatcher.Emit(webhook.NewEvent(webhook.OutlierEjected, routeID, map[string]interface{}{
					"backend": backend, "reason": reason,
				}))
			},
			func(routeID, backend string) {
				g.webhookDispatcher.Emit(webhook.NewEvent(webhook.OutlierRecovered, routeID, map[string]interface{}{
					"backend": backend,
				}))
			},
		)
	}

	// Initialize global IP filter
	if cfg.IPFilter.Enabled {
		var err error
		s.globalIPFilter, err = ipfilter.New(cfg.IPFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global IP filter: %w", err)
		}
	}

	// Initialize global rules engine
	if len(cfg.Rules.Request) > 0 || len(cfg.Rules.Response) > 0 {
		var err error
		s.globalRules, err = rules.NewEngine(cfg.Rules.Request, cfg.Rules.Response)
		if err != nil {
			return nil, fmt.Errorf("failed to compile global rules: %w", err)
		}
	}

	// Initialize auth
	if cfg.Authentication.APIKey.Enabled {
		s.apiKeyAuth = auth.NewAPIKeyAuth(cfg.Authentication.APIKey)
	}
	if cfg.Authentication.JWT.Enabled {
		var err error
		s.jwtAuth, err = auth.NewJWTAuth(cfg.Authentication.JWT)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize JWT auth: %w", err)
		}
	}
	if cfg.Authentication.OAuth.Enabled {
		var err error
		s.oauthAuth, err = auth.NewOAuthAuth(cfg.Authentication.OAuth)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize OAuth auth: %w", err)
		}
	}

	// Rebuild transport pool from new config and swap onto shared proxy
	newPool := g.buildTransportPool(cfg)
	oldPool := g.proxy.GetTransportPool()
	g.proxy.SetTransportPool(newPool)
	if oldPool != nil {
		oldPool.CloseIdleConnections()
	}

	// Initialize each route using a temporary Gateway view so addRouteForState works
	for _, routeCfg := range cfg.Routes {
		if err := g.addRouteForState(s, routeCfg); err != nil {
			// Clean up translators on failure
			s.translators.Close()
			return nil, fmt.Errorf("failed to add route %s: %w", routeCfg.ID, err)
		}
	}

	return s, nil
}

// addRouteForState adds a single route into the given gatewayState, using the Gateway's
// shared infrastructure (proxy, healthChecker, registry, redisClient).
func (g *Gateway) addRouteForState(s *gatewayState, routeCfg config.RouteConfig) error {
	// Resolve upstream references into inline backends/service/LB settings
	routeCfg = resolveUpstreamRefs(s.config, routeCfg)

	if err := s.router.AddRoute(routeCfg); err != nil {
		return err
	}

	route := s.router.GetRoute(routeCfg.ID)
	if route == nil {
		return fmt.Errorf("route not found after adding: %s", routeCfg.ID)
	}

	// Set upstream name on route for transport pool resolution
	route.UpstreamName = routeCfg.Upstream

	var backends []*loadbalancer.Backend

	if routeCfg.Service.Name != "" {
		ctx := context.Background()
		services, err := g.registry.DiscoverWithTags(ctx, routeCfg.Service.Name, routeCfg.Service.Tags)
		if err != nil {
			logging.Warn("Failed to discover service during reload",
				zap.String("service", routeCfg.Service.Name),
				zap.Error(err),
			)
		}
		for _, svc := range services {
			backends = append(backends, &loadbalancer.Backend{
				URL:     svc.URL(),
				Weight:  1,
				Healthy: svc.Health == registry.HealthPassing,
			})
		}

		// Watch service in the context of the new state
		watchCtx, cancel := context.WithCancel(context.Background())
		s.watchCancels[routeCfg.ID] = cancel
		go g.watchServiceForState(s, watchCtx, routeCfg.ID, routeCfg.Service.Name, routeCfg.Service.Tags)
	} else {
		var usHC *config.HealthCheckConfig
		if routeCfg.Upstream != "" {
			if us, ok := s.config.Upstreams[routeCfg.Upstream]; ok {
				usHC = us.HealthCheck
			}
		}
		for _, b := range routeCfg.Backends {
			weight := b.Weight
			if weight == 0 {
				weight = 1
			}
			backends = append(backends, &loadbalancer.Backend{
				URL:     b.URL,
				Weight:  weight,
				Healthy: true,
			})

			// Register backend with health checker
			g.healthChecker.UpdateBackend(upstreamHealthCheck(b.URL, s.config.HealthCheck, usHC, b.HealthCheck))
		}
	}

	// Create route proxy
	if routeCfg.Versioning.Enabled {
		versionBackends := make(map[string][]*loadbalancer.Backend)
		for ver, vcfg := range routeCfg.Versioning.Versions {
			var vBacks []*loadbalancer.Backend
			var verUSHC *config.HealthCheckConfig
			if vcfg.Upstream != "" {
				if us, ok := s.config.Upstreams[vcfg.Upstream]; ok {
					verUSHC = us.HealthCheck
				}
			}
			for _, b := range vcfg.Backends {
				weight := b.Weight
				if weight == 0 {
					weight = 1
				}
				vBacks = append(vBacks, &loadbalancer.Backend{URL: b.URL, Weight: weight, Healthy: true})
				g.healthChecker.UpdateBackend(upstreamHealthCheck(b.URL, s.config.HealthCheck, verUSHC, b.HealthCheck))
			}
			versionBackends[ver] = vBacks
		}
		vb := loadbalancer.NewVersionedBalancer(versionBackends, routeCfg.Versioning.DefaultVersion)
		s.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, vb)
	} else if len(routeCfg.TrafficSplit) > 0 {
		var wb *loadbalancer.WeightedBalancer
		if routeCfg.Sticky.Enabled {
			wb = loadbalancer.NewWeightedBalancerWithSticky(routeCfg.TrafficSplit, routeCfg.Sticky)
		} else {
			wb = loadbalancer.NewWeightedBalancer(routeCfg.TrafficSplit)
		}
		s.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, wb)
	} else {
		balancer := createBalancer(routeCfg, backends)
		s.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, balancer)
	}

	// Rate limiting
	if routeCfg.RateLimit.Enabled || routeCfg.RateLimit.Rate > 0 {
		if routeCfg.RateLimit.Mode == "distributed" && g.redisClient != nil {
			s.rateLimiters.AddRouteDistributed(routeCfg.ID, ratelimit.RedisLimiterConfig{
				Client: g.redisClient,
				Prefix: "gw:rl:" + routeCfg.ID + ":",
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
			})
		} else if routeCfg.RateLimit.Algorithm == "sliding_window" {
			s.rateLimiters.AddRouteSlidingWindow(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
			})
		} else {
			s.rateLimiters.AddRoute(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
			})
		}
	}

	// gRPC handler
	if routeCfg.GRPC.Enabled {
		s.grpcHandlers[routeCfg.ID] = grpcproxy.New(true)
	}

	// Protocol translator
	if routeCfg.Protocol.Type != "" {
		bal := s.routeProxies[routeCfg.ID].GetBalancer()
		if err := s.translators.AddRoute(routeCfg.ID, routeCfg.Protocol, bal); err != nil {
			return fmt.Errorf("protocol translator: route %s: %w", routeCfg.ID, err)
		}
	}

	// Generic features
	for _, f := range s.features {
		if err := f.Setup(routeCfg.ID, routeCfg); err != nil {
			return fmt.Errorf("feature %s: route %s: %w", f.Name(), routeCfg.ID, err)
		}
	}

	// Override per-try timeout with backend timeout when configured
	if routeCfg.TimeoutPolicy.Backend > 0 {
		s.routeProxies[routeCfg.ID].SetPerTryTimeout(routeCfg.TimeoutPolicy.Backend)
	}

	// Canary setup (needs WeightedBalancer)
	if routeCfg.Canary.Enabled {
		if wb, ok := s.routeProxies[routeCfg.ID].GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			if err := s.canaryControllers.AddRoute(routeCfg.ID, routeCfg.Canary, wb); err != nil {
				return fmt.Errorf("canary: route %s: %w", routeCfg.ID, err)
			}
		}
	}

	// Outlier detection setup (needs Balancer)
	if routeCfg.OutlierDetection.Enabled {
		s.outlierDetectors.AddRoute(routeCfg.ID, routeCfg.OutlierDetection, s.routeProxies[routeCfg.ID].GetBalancer())
	}

	// Build middleware pipeline - we need a temporary gateway-like context
	handler := g.buildRouteHandlerForState(s, routeCfg.ID, routeCfg, route, s.routeProxies[routeCfg.ID])
	s.routeHandlers[routeCfg.ID] = handler

	return nil
}

// watchServiceForState is like watchService but writes to a gatewayState's routeProxies.
func (g *Gateway) watchServiceForState(s *gatewayState, ctx context.Context, routeID, serviceName string, tags []string) {
	ch, err := g.registry.Watch(ctx, serviceName)
	if err != nil {
		logging.Error("Failed to watch service during reload",
			zap.String("service", serviceName),
			zap.Error(err),
		)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case services, ok := <-ch:
			if !ok {
				return
			}

			var filtered []*registry.Service
			for _, svc := range services {
				if hasAllTags(svc.Tags, tags) {
					filtered = append(filtered, svc)
				}
			}

			var backends []*loadbalancer.Backend
			for _, svc := range filtered {
				backends = append(backends, &loadbalancer.Backend{
					URL:     svc.URL(),
					Weight:  1,
					Healthy: svc.Health == registry.HealthPassing,
				})
			}

			// The state's routeProxies are accessed by the Gateway under g.mu,
			// but since this watcher was started for the new state it's safe to
			// read from it directly — the map doesn't change after buildState returns.
			if rp, ok := s.routeProxies[routeID]; ok {
				rp.UpdateBackends(backends)
			}
		}
	}
}

// buildRouteHandlerForState is like buildRouteHandler but reads from a gatewayState.
func (g *Gateway) buildRouteHandlerForState(s *gatewayState, routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler {
	// We temporarily swap the Gateway's fields to build the handler, then swap back.
	// This is safe because buildState is called without holding g.mu.
	// Since buildRouteHandler reads from g's field directly, we do a field-by-field swap.
	// Alternative: refactor buildRouteHandler to take a state param. But to minimize diff,
	// we reuse the existing method by pointing g's fields at the new state's managers.
	//
	// However, this approach is fragile. Instead, let's just call buildRouteHandler but
	// override the fields that differ. Since we can't hold both old and new state,
	// we pass through the real method after temporarily installing state managers.

	// Save old state
	oldIPFilters := g.ipFilters
	oldGlobalIPFilter := g.globalIPFilter
	oldCorsHandlers := g.corsHandlers
	oldRateLimiters := g.rateLimiters
	oldThrottlers := g.throttlers
	oldPriorityAdmitter := g.priorityAdmitter
	oldPriorityConfigs := g.priorityConfigs
	oldGlobalRules := g.globalRules
	oldRouteRules := g.routeRules
	oldWafHandlers := g.wafHandlers
	oldFaultInjectors := g.faultInjectors
	oldBandwidthLimiters := g.bandwidthLimiters
	oldValidators := g.validators
	oldCaches := g.caches
	oldCircuitBreakers := g.circuitBreakers
	oldCompressors := g.compressors
	oldMirrors := g.mirrors
	oldGrpcHandlers := g.grpcHandlers
	oldTranslators := g.translators
	oldGraphqlParsers := g.graphqlParsers
	oldCoalescers := g.coalescers
	oldCanaryControllers := g.canaryControllers
	oldAdaptiveLimiters := g.adaptiveLimiters
	oldExtAuths := g.extAuths
	oldVersioners := g.versioners
	oldAccessLogConfigs := g.accessLogConfigs
	oldOpenAPIValidators := g.openapiValidators
	oldTimeoutConfigs := g.timeoutConfigs
	oldErrorPages := g.errorPages
	oldNonceCheckers := g.nonceCheckers
	oldOutlierDetectors := g.outlierDetectors
	oldIdempotencyHandlers := g.idempotencyHandlers
	oldBackendSigners := g.backendSigners
	oldDecompressors := g.decompressors
	oldSecurityHeaders := g.securityHeaders

	// Install new state
	g.ipFilters = s.ipFilters
	g.globalIPFilter = s.globalIPFilter
	g.corsHandlers = s.corsHandlers
	g.rateLimiters = s.rateLimiters
	g.throttlers = s.throttlers
	g.priorityAdmitter = s.priorityAdmitter
	g.priorityConfigs = s.priorityConfigs
	g.globalRules = s.globalRules
	g.routeRules = s.routeRules
	g.wafHandlers = s.wafHandlers
	g.faultInjectors = s.faultInjectors
	g.bandwidthLimiters = s.bandwidthLimiters
	g.validators = s.validators
	g.caches = s.caches
	g.circuitBreakers = s.circuitBreakers
	g.compressors = s.compressors
	g.mirrors = s.mirrors
	g.grpcHandlers = s.grpcHandlers
	g.translators = s.translators
	g.graphqlParsers = s.graphqlParsers
	g.coalescers = s.coalescers
	g.canaryControllers = s.canaryControllers
	g.adaptiveLimiters = s.adaptiveLimiters
	g.extAuths = s.extAuths
	g.versioners = s.versioners
	g.accessLogConfigs = s.accessLogConfigs
	g.openapiValidators = s.openapiValidators
	g.timeoutConfigs = s.timeoutConfigs
	g.errorPages = s.errorPages
	g.nonceCheckers = s.nonceCheckers
	g.outlierDetectors = s.outlierDetectors
	g.idempotencyHandlers = s.idempotencyHandlers
	g.backendSigners = s.backendSigners
	g.decompressors = s.decompressors
	g.securityHeaders = s.securityHeaders

	handler := g.buildRouteHandler(routeID, cfg, route, rp)

	// Restore old state
	g.ipFilters = oldIPFilters
	g.globalIPFilter = oldGlobalIPFilter
	g.corsHandlers = oldCorsHandlers
	g.rateLimiters = oldRateLimiters
	g.throttlers = oldThrottlers
	g.priorityAdmitter = oldPriorityAdmitter
	g.priorityConfigs = oldPriorityConfigs
	g.globalRules = oldGlobalRules
	g.routeRules = oldRouteRules
	g.wafHandlers = oldWafHandlers
	g.faultInjectors = oldFaultInjectors
	g.bandwidthLimiters = oldBandwidthLimiters
	g.validators = oldValidators
	g.caches = oldCaches
	g.circuitBreakers = oldCircuitBreakers
	g.compressors = oldCompressors
	g.mirrors = oldMirrors
	g.grpcHandlers = oldGrpcHandlers
	g.translators = oldTranslators
	g.graphqlParsers = oldGraphqlParsers
	g.coalescers = oldCoalescers
	g.canaryControllers = oldCanaryControllers
	g.adaptiveLimiters = oldAdaptiveLimiters
	g.extAuths = oldExtAuths
	g.versioners = oldVersioners
	g.accessLogConfigs = oldAccessLogConfigs
	g.openapiValidators = oldOpenAPIValidators
	g.timeoutConfigs = oldTimeoutConfigs
	g.errorPages = oldErrorPages
	g.nonceCheckers = oldNonceCheckers
	g.outlierDetectors = oldOutlierDetectors
	g.idempotencyHandlers = oldIdempotencyHandlers
	g.backendSigners = oldBackendSigners
	g.decompressors = oldDecompressors
	g.securityHeaders = oldSecurityHeaders

	return handler
}

// Reload atomically replaces all route-scoped state with a new config.
// Shared infrastructure (proxy, healthChecker, registry, metricsCollector, redisClient, tracer) is preserved.
// In-flight requests complete with the old handler (handler refs are grabbed under RLock).
func (g *Gateway) Reload(newCfg *config.Config) ReloadResult {
	result := ReloadResult{Timestamp: time.Now()}

	// Build new state (no locks held)
	newState, err := g.buildState(newCfg)
	if err != nil {
		result.Error = err.Error()
		if g.webhookDispatcher != nil {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.ConfigReloadFailure, "", map[string]interface{}{
				"error": err.Error(),
			}))
		}
		return result
	}

	// Compute changes
	result.Changes = diffConfig(g.config, newCfg)

	// Save old state for cleanup
	oldConfig := g.config
	_ = oldConfig
	oldWatchCancels := g.watchCancels
	oldTranslators := g.translators
	oldJWT := g.jwtAuth
	oldCanaryControllers := g.canaryControllers
	oldAdaptiveLimiters := g.adaptiveLimiters
	oldExtAuths := g.extAuths
	oldNonceCheckers := g.nonceCheckers
	oldOutlierDetectors := g.outlierDetectors
	oldIdempotencyHandlers := g.idempotencyHandlers
	oldBackendSigners := g.backendSigners

	// Swap all state under write lock
	g.mu.Lock()
	g.config = newState.config
	g.router = newState.router
	g.routeProxies = newState.routeProxies
	g.routeHandlers = newState.routeHandlers
	g.watchCancels = newState.watchCancels
	g.features = newState.features
	g.circuitBreakers = newState.circuitBreakers
	g.caches = newState.caches
	g.ipFilters = newState.ipFilters
	g.globalIPFilter = newState.globalIPFilter
	g.corsHandlers = newState.corsHandlers
	g.compressors = newState.compressors
	g.validators = newState.validators
	g.mirrors = newState.mirrors
	g.routeRules = newState.routeRules
	g.globalRules = newState.globalRules
	g.throttlers = newState.throttlers
	g.bandwidthLimiters = newState.bandwidthLimiters
	g.priorityAdmitter = newState.priorityAdmitter
	g.priorityConfigs = newState.priorityConfigs
	g.faultInjectors = newState.faultInjectors
	g.wafHandlers = newState.wafHandlers
	g.graphqlParsers = newState.graphqlParsers
	g.coalescers = newState.coalescers
	g.canaryControllers = newState.canaryControllers
	g.adaptiveLimiters = newState.adaptiveLimiters
	g.extAuths = newState.extAuths
	g.versioners = newState.versioners
	g.accessLogConfigs = newState.accessLogConfigs
	g.openapiValidators = newState.openapiValidators
	g.timeoutConfigs = newState.timeoutConfigs
	g.errorPages = newState.errorPages
	g.nonceCheckers = newState.nonceCheckers
	g.csrfProtectors = newState.csrfProtectors
	g.outlierDetectors = newState.outlierDetectors
	g.idempotencyHandlers = newState.idempotencyHandlers
	g.backendSigners = newState.backendSigners
	g.decompressors = newState.decompressors
	g.securityHeaders = newState.securityHeaders
	g.translators = newState.translators
	g.rateLimiters = newState.rateLimiters
	g.grpcHandlers = newState.grpcHandlers
	g.apiKeyAuth = newState.apiKeyAuth
	g.jwtAuth = newState.jwtAuth
	g.oauthAuth = newState.oauthAuth
	g.mu.Unlock()

	// Clean up old state (after releasing lock — in-flight requests already hold handler refs)
	for _, cancel := range oldWatchCancels {
		cancel()
	}
	oldTranslators.Close()
	oldExtAuths.CloseAll()
	oldCanaryControllers.StopAll()
	oldAdaptiveLimiters.StopAll()
	oldNonceCheckers.CloseAll()
	oldOutlierDetectors.StopAll()
	oldIdempotencyHandlers.CloseAll()
	_ = oldBackendSigners // no cleanup needed (stateless)
	if oldJWT != nil {
		oldJWT.Close()
	}

	// Reconcile health checker: remove backends no longer present
	newBackendURLs := make(map[string]bool)
	// Collect backend URLs from upstreams
	for _, us := range newCfg.Upstreams {
		for _, b := range us.Backends {
			newBackendURLs[b.URL] = true
		}
	}
	for _, routeCfg := range newCfg.Routes {
		// Resolve upstream refs to find effective backends
		resolved := resolveUpstreamRefs(newCfg, routeCfg)
		for _, b := range resolved.Backends {
			newBackendURLs[b.URL] = true
		}
		for _, split := range resolved.TrafficSplit {
			for _, b := range split.Backends {
				newBackendURLs[b.URL] = true
			}
		}
		if resolved.Versioning.Enabled {
			for _, vcfg := range resolved.Versioning.Versions {
				for _, b := range vcfg.Backends {
					newBackendURLs[b.URL] = true
				}
			}
		}
		if resolved.Mirror.Enabled {
			for _, b := range resolved.Mirror.Backends {
				newBackendURLs[b.URL] = true
			}
		}
	}
	for url := range g.healthChecker.GetAllStatus() {
		if !newBackendURLs[url] {
			g.healthChecker.RemoveBackend(url)
		}
	}

	// Update webhook endpoints and emit success event
	if g.webhookDispatcher != nil {
		g.webhookDispatcher.UpdateEndpoints(newCfg.Webhooks.Endpoints)
		g.webhookDispatcher.Emit(webhook.NewEvent(webhook.ConfigReloadSuccess, "", map[string]interface{}{
			"changes": result.Changes,
		}))
	}

	result.Success = true
	return result
}

// diffConfig returns a list of human-readable changes between old and new configs.
func diffConfig(oldCfg, newCfg *config.Config) []string {
	var changes []string

	oldRoutes := make(map[string]bool, len(oldCfg.Routes))
	for _, r := range oldCfg.Routes {
		oldRoutes[r.ID] = true
	}
	newRoutes := make(map[string]bool, len(newCfg.Routes))
	for _, r := range newCfg.Routes {
		newRoutes[r.ID] = true
	}

	// Added routes
	for id := range newRoutes {
		if !oldRoutes[id] {
			changes = append(changes, fmt.Sprintf("route added: %s", id))
		}
	}
	// Removed routes
	for id := range oldRoutes {
		if !newRoutes[id] {
			changes = append(changes, fmt.Sprintf("route removed: %s", id))
		}
	}
	// Modified routes (in both old and new)
	for id := range newRoutes {
		if oldRoutes[id] {
			changes = append(changes, fmt.Sprintf("route reloaded: %s", id))
		}
	}

	// Listener changes
	if len(oldCfg.Listeners) != len(newCfg.Listeners) {
		changes = append(changes, fmt.Sprintf("listeners changed: %d -> %d", len(oldCfg.Listeners), len(newCfg.Listeners)))
	}

	sort.Strings(changes)
	return changes
}

// wsProxy accessor for buildState — uses shared wsProxy from Gateway
func (g *Gateway) getWSProxy() *websocket.Proxy {
	return g.wsProxy
}

// resolver accessor for buildState
func (g *Gateway) getResolver() *variables.Resolver {
	return g.resolver
}
