package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/example/gateway/internal/logging"
	"go.uber.org/zap"

	"github.com/example/gateway/internal/cache"
	"github.com/example/gateway/internal/circuitbreaker"
	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/graphql"
	"github.com/example/gateway/internal/health"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/metrics"
	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/middleware/auth"
	"github.com/example/gateway/internal/middleware/compression"
	"github.com/example/gateway/internal/middleware/cors"
	"github.com/example/gateway/internal/middleware/mtls"
	"github.com/example/gateway/internal/middleware/ipfilter"
	"github.com/example/gateway/internal/middleware/ratelimit"
	"github.com/example/gateway/internal/middleware/validation"
	"github.com/example/gateway/internal/middleware/waf"
	"github.com/example/gateway/internal/mirror"
	grpcproxy "github.com/example/gateway/internal/proxy/grpc"
	"github.com/example/gateway/internal/proxy/protocol"
	"github.com/example/gateway/internal/rules"
	"github.com/example/gateway/internal/trafficshape"
	"github.com/example/gateway/internal/proxy"
	"github.com/example/gateway/internal/registry"
	"github.com/redis/go-redis/v9"
	"github.com/example/gateway/internal/registry/consul"
	"github.com/example/gateway/internal/registry/etcd"
	"github.com/example/gateway/internal/registry/memory"
	"github.com/example/gateway/internal/retry"
	"github.com/example/gateway/internal/router"
	"github.com/example/gateway/internal/tracing"
	"github.com/example/gateway/internal/variables"
	"github.com/example/gateway/internal/websocket"
)

// Gateway is the main API gateway
type Gateway struct {
	config        *config.Config
	router        *router.Router
	proxy         *proxy.Proxy
	registry      registry.Registry
	healthChecker *health.Checker
	apiKeyAuth    *auth.APIKeyAuth
	jwtAuth       *auth.JWTAuth
	oauthAuth     *auth.OAuthAuth
	rateLimiters  *ratelimit.RateLimitByRoute
	resolver      *variables.Resolver

	// New feature managers
	circuitBreakers *circuitbreaker.BreakerByRoute
	caches          *cache.CacheByRoute
	wsProxy         *websocket.Proxy

	// Feature managers (Batch 1-3)
	ipFilters      *ipfilter.IPFilterByRoute
	globalIPFilter *ipfilter.Filter
	corsHandlers   *cors.CORSByRoute
	compressors    *compression.CompressorByRoute
	metricsCollector *metrics.Collector
	validators     *validation.ValidatorByRoute
	mirrors        *mirror.MirrorByRoute
	tracer         *tracing.Tracer

	grpcHandlers map[string]*grpcproxy.Handler
	translators  *protocol.TranslatorByRoute

	globalRules *rules.RuleEngine
	routeRules  *rules.RulesByRoute

	// Traffic shaping managers
	throttlers        *trafficshape.ThrottleByRoute
	bandwidthLimiters *trafficshape.BandwidthByRoute
	priorityAdmitter  *trafficshape.PriorityAdmitter // shared across routes, nil if disabled
	priorityConfigs   *trafficshape.PriorityByRoute
	faultInjectors    *trafficshape.FaultInjectionByRoute
	wafHandlers       *waf.WAFByRoute
	graphqlParsers    *graphql.GraphQLByRoute

	features []Feature

	redisClient *redis.Client // shared Redis client for distributed features

	routeProxies  map[string]*proxy.RouteProxy
	routeHandlers map[string]http.Handler
	watchCancels  map[string]context.CancelFunc
	mu            sync.RWMutex
}

// statusRecorder wraps http.ResponseWriter to capture the status code
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.statusCode = code
	sr.ResponseWriter.WriteHeader(code)
}

// StatusCode implements StatusCapture.
func (sr *statusRecorder) StatusCode() int {
	return sr.statusCode
}

// Flush implements http.Flusher, forwarding to the underlying ResponseWriter if supported.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// New creates a new gateway
func New(cfg *config.Config) (*Gateway, error) {
	g := &Gateway{
		config:           cfg,
		router:           router.New(),
		rateLimiters:     ratelimit.NewRateLimitByRoute(),
		resolver:         variables.NewResolver(),
		circuitBreakers:  circuitbreaker.NewBreakerByRoute(),
		caches:           cache.NewCacheByRoute(),
		wsProxy:          websocket.NewProxy(config.WebSocketConfig{}),
		ipFilters:        ipfilter.NewIPFilterByRoute(),
		corsHandlers:     cors.NewCORSByRoute(),
		compressors:      compression.NewCompressorByRoute(),
		metricsCollector: metrics.NewCollector(),
		validators:       validation.NewValidatorByRoute(),
		mirrors:          mirror.NewMirrorByRoute(),
		grpcHandlers:     make(map[string]*grpcproxy.Handler),
		translators:       protocol.NewTranslatorByRoute(),
		routeRules:        rules.NewRulesByRoute(),
		throttlers:        trafficshape.NewThrottleByRoute(),
		bandwidthLimiters: trafficshape.NewBandwidthByRoute(),
		priorityConfigs:   trafficshape.NewPriorityByRoute(),
		faultInjectors:    trafficshape.NewFaultInjectionByRoute(),
		wafHandlers:       waf.NewWAFByRoute(),
		graphqlParsers:    graphql.NewGraphQLByRoute(),
		routeProxies:      make(map[string]*proxy.RouteProxy),
		routeHandlers:    make(map[string]http.Handler),
		watchCancels:     make(map[string]context.CancelFunc),
	}

	// Initialize shared priority admitter if global priority is enabled
	if cfg.TrafficShaping.Priority.Enabled {
		g.priorityAdmitter = trafficshape.NewPriorityAdmitter(cfg.TrafficShaping.Priority.MaxConcurrent)
	}

	// Register features for generic setup iteration
	g.features = []Feature{
		&ipFilterFeature{g.ipFilters},
		&corsFeature{g.corsHandlers},
		&circuitBreakerFeature{g.circuitBreakers},
		&cacheFeature{g.caches},
		&compressionFeature{g.compressors},
		&validationFeature{g.validators},
		&mirrorFeature{g.mirrors},
		&rulesFeature{g.routeRules},
		&throttleFeature{m: g.throttlers, global: &cfg.TrafficShaping.Throttle},
		&bandwidthFeature{m: g.bandwidthLimiters, global: &cfg.TrafficShaping.Bandwidth},
		&priorityFeature{m: g.priorityConfigs, global: &cfg.TrafficShaping.Priority},
		&faultInjectionFeature{m: g.faultInjectors, global: &cfg.TrafficShaping.FaultInjection},
		&wafFeature{g.wafHandlers},
		&graphqlFeature{g.graphqlParsers},
	}

	// Initialize global IP filter
	if cfg.IPFilter.Enabled {
		var err error
		g.globalIPFilter, err = ipfilter.New(cfg.IPFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global IP filter: %w", err)
		}
	}

	// Initialize global rules engine
	if len(cfg.Rules.Request) > 0 || len(cfg.Rules.Response) > 0 {
		var err error
		g.globalRules, err = rules.NewEngine(cfg.Rules.Request, cfg.Rules.Response)
		if err != nil {
			return nil, fmt.Errorf("failed to compile global rules: %w", err)
		}
	}

	// Initialize tracer
	if cfg.Tracing.Enabled {
		var err error
		g.tracer, err = tracing.New(cfg.Tracing)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize tracer: %w", err)
		}
	}

	// Initialize Redis client if configured
	if cfg.Redis.Address != "" {
		g.redisClient = redis.NewClient(&redis.Options{
			Addr:        cfg.Redis.Address,
			Password:    cfg.Redis.Password,
			DB:          cfg.Redis.DB,
			PoolSize:    cfg.Redis.PoolSize,
			DialTimeout: cfg.Redis.DialTimeout,
		})
	}

	// Initialize health checker
	g.healthChecker = health.NewChecker(health.Config{
		OnChange: func(url string, status health.Status) {
			logging.Info("Backend health changed",
				zap.String("backend", url),
				zap.String("status", string(status)),
			)
			g.updateBackendHealth(url, status)
		},
	})

	// Initialize proxy with optional custom DNS resolver
	var transport *http.Transport
	if len(cfg.DNSResolver.Nameservers) > 0 {
		resolver := proxy.NewResolver(cfg.DNSResolver.Nameservers, cfg.DNSResolver.Timeout)
		tcfg := proxy.DefaultTransportConfig
		tcfg.Resolver = resolver
		transport = proxy.NewTransport(tcfg)
	}
	g.proxy = proxy.New(proxy.Config{
		Transport:     transport,
		HealthChecker: g.healthChecker,
	})

	// Initialize registry
	if err := g.initRegistry(); err != nil {
		return nil, fmt.Errorf("failed to initialize registry: %w", err)
	}

	// Initialize authentication
	if err := g.initAuth(); err != nil {
		return nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Initialize routes
	if err := g.initRoutes(); err != nil {
		return nil, fmt.Errorf("failed to initialize routes: %w", err)
	}

	return g, nil
}

// initRegistry initializes the service registry
func (g *Gateway) initRegistry() error {
	var err error

	switch g.config.Registry.Type {
	case "consul":
		g.registry, err = consul.New(g.config.Registry.Consul)
	case "etcd":
		g.registry, err = etcd.New(g.config.Registry.Etcd)
	case "memory":
		if g.config.Registry.Memory.APIEnabled {
			g.registry, err = memory.NewWithAPI(g.config.Registry.Memory.APIPort)
		} else {
			g.registry = memory.New()
		}
	default:
		g.registry = memory.New()
	}

	return err
}

// initAuth initializes authentication providers
func (g *Gateway) initAuth() error {
	// Initialize API Key auth
	if g.config.Authentication.APIKey.Enabled {
		g.apiKeyAuth = auth.NewAPIKeyAuth(g.config.Authentication.APIKey)
	}

	// Initialize JWT auth
	if g.config.Authentication.JWT.Enabled {
		var err error
		g.jwtAuth, err = auth.NewJWTAuth(g.config.Authentication.JWT)
		if err != nil {
			return err
		}
	}

	// Initialize OAuth auth
	if g.config.Authentication.OAuth.Enabled {
		var err error
		g.oauthAuth, err = auth.NewOAuthAuth(g.config.Authentication.OAuth)
		if err != nil {
			return err
		}
	}

	return nil
}

// initRoutes initializes all routes from configuration
func (g *Gateway) initRoutes() error {
	for _, routeCfg := range g.config.Routes {
		if err := g.addRoute(routeCfg); err != nil {
			return fmt.Errorf("failed to add route %s: %w", routeCfg.ID, err)
		}
	}
	return nil
}

// addRoute adds a single route
func (g *Gateway) addRoute(routeCfg config.RouteConfig) error {
	// Add route to router
	if err := g.router.AddRoute(routeCfg); err != nil {
		return err
	}

	route := g.router.GetRoute(routeCfg.ID)
	if route == nil {
		return fmt.Errorf("route not found after adding: %s", routeCfg.ID)
	}

	// Set up backends
	var backends []*loadbalancer.Backend

	// Check if using service discovery
	if routeCfg.Service.Name != "" {
		ctx := context.Background()

		// Discover initial backends
		services, err := g.registry.DiscoverWithTags(ctx, routeCfg.Service.Name, routeCfg.Service.Tags)
		if err != nil {
			logging.Warn("Failed to discover service",
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

		// Start watching for changes
		g.watchService(routeCfg.ID, routeCfg.Service.Name, routeCfg.Service.Tags)
	} else {
		// Use static backends
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

			// Add to health checker
			g.healthChecker.AddBackend(health.Backend{
				URL:        b.URL,
				HealthPath: "/health",
			})
		}
	}

	// Create route proxy with the appropriate balancer
	g.mu.Lock()
	if len(routeCfg.TrafficSplit) > 0 {
		var wb *loadbalancer.WeightedBalancer
		if routeCfg.Sticky.Enabled {
			wb = loadbalancer.NewWeightedBalancerWithSticky(routeCfg.TrafficSplit, routeCfg.Sticky)
		} else {
			wb = loadbalancer.NewWeightedBalancer(routeCfg.TrafficSplit)
		}
		g.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, wb)
	} else {
		balancer := createBalancer(routeCfg, backends)
		g.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, balancer)
	}
	g.mu.Unlock()

	// Set up rate limiting (unique setup signature, not in feature loop)
	if routeCfg.RateLimit.Enabled || routeCfg.RateLimit.Rate > 0 {
		if routeCfg.RateLimit.Mode == "distributed" && g.redisClient != nil {
			g.rateLimiters.AddRouteDistributed(routeCfg.ID, ratelimit.RedisLimiterConfig{
				Client: g.redisClient,
				Prefix: "gw:rl:" + routeCfg.ID + ":",
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
			})
		} else {
			g.rateLimiters.AddRoute(routeCfg.ID, ratelimit.Config{
				Rate:   routeCfg.RateLimit.Rate,
				Period: routeCfg.RateLimit.Period,
				Burst:  routeCfg.RateLimit.Burst,
				PerIP:  routeCfg.RateLimit.PerIP,
			})
		}
	}

	// Set up gRPC handler (unique setup signature, not in feature loop)
	if routeCfg.GRPC.Enabled {
		g.grpcHandlers[routeCfg.ID] = grpcproxy.New(true)
	}

	// Set up protocol translator (replaces RouteProxy as innermost handler)
	if routeCfg.Protocol.Type != "" {
		balancer := g.routeProxies[routeCfg.ID].GetBalancer()
		if err := g.translators.AddRoute(routeCfg.ID, routeCfg.Protocol, balancer); err != nil {
			return fmt.Errorf("protocol translator: route %s: %w", routeCfg.ID, err)
		}
	}

	// Set up all features generically
	for _, f := range g.features {
		if err := f.Setup(routeCfg.ID, routeCfg); err != nil {
			return fmt.Errorf("feature %s: route %s: %w", f.Name(), routeCfg.ID, err)
		}
	}

	// Build the per-route middleware pipeline handler
	handler := g.buildRouteHandler(routeCfg.ID, routeCfg, route, g.routeProxies[routeCfg.ID])
	g.mu.Lock()
	g.routeHandlers[routeCfg.ID] = handler
	g.mu.Unlock()

	return nil
}

// createBalancer creates a load balancer for the given route config and backends.
func createBalancer(cfg config.RouteConfig, backends []*loadbalancer.Backend) loadbalancer.Balancer {
	switch cfg.LoadBalancer {
	case "least_conn":
		return loadbalancer.NewLeastConnections(backends)
	case "consistent_hash":
		return loadbalancer.NewConsistentHash(backends, cfg.ConsistentHash)
	case "least_response_time":
		return loadbalancer.NewLeastResponseTime(backends)
	default:
		return loadbalancer.NewRoundRobin(backends)
	}
}

// buildRouteHandler constructs the per-route middleware pipeline.
// Chain ordering matches CLAUDE.md serveHTTP flow exactly.
func (g *Gateway) buildRouteHandler(routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler {
	chain := middleware.NewBuilder()

	// 1. metricsMW — outermost: timing + status (Step 13)
	chain = chain.Use(metricsMW(g.metricsCollector, routeID))

	// 2. ipFilterMW — global + per-route (Step 1.1)
	routeIPFilter := g.ipFilters.GetFilter(routeID)
	if g.globalIPFilter != nil || routeIPFilter != nil {
		chain = chain.Use(ipFilterMW(g.globalIPFilter, routeIPFilter))
	}

	// 3. corsMW — preflight + headers (Step 1.5)
	if corsHandler := g.corsHandlers.GetHandler(routeID); corsHandler != nil && corsHandler.IsEnabled() {
		chain = chain.Use(corsMW(corsHandler))
	}

	// 4. varContextMW — set RouteID + PathParams (Step 2)
	chain = chain.Use(varContextMW(routeID))

	// 5. rateLimitMW — per-route limiter (local or distributed)
	if mw := g.rateLimiters.GetMiddleware(routeID); mw != nil {
		chain = chain.Use(mw)
	}

	// 5.5 throttleMW — delay/queue (after reject, before auth)
	if t := g.throttlers.GetThrottler(routeID); t != nil {
		chain = chain.Use(throttleMW(t))
	}

	// 6. authMW — authenticate (Step 3)
	if route.Auth.Required {
		chain = chain.Use(authMW(g, route.Auth))
	}

	// 6.5 priorityMW — admission control (after auth, needs Identity)
	if g.priorityAdmitter != nil {
		if pcfg, ok := g.priorityConfigs.GetConfig(routeID); ok {
			chain = chain.Use(priorityMW(g.priorityAdmitter, pcfg))
		}
	}

	// 7. requestRulesMW — global + per-route (Step 3.5)
	routeEngine := g.routeRules.GetEngine(routeID)
	hasReqRules := (g.globalRules != nil && g.globalRules.HasRequestRules()) ||
		(routeEngine != nil && routeEngine.HasRequestRules())
	if hasReqRules {
		chain = chain.Use(requestRulesMW(g.globalRules, routeEngine))
	}

	// 7.25 wafMW — WAF inspection (after request rules, before fault injection)
	if wafHandler := g.wafHandlers.GetWAF(routeID); wafHandler != nil {
		chain = chain.Use(wafMW(wafHandler))
	}

	// 7.5. faultInjectionMW — inject delays/aborts (Step 7.5)
	if fi := g.faultInjectors.GetInjector(routeID); fi != nil {
		chain = chain.Use(faultInjectionMW(fi))
	}

	// 8. bodyLimitMW — MaxBodySize (Step 4.5)
	if route.MaxBodySize > 0 {
		chain = chain.Use(bodyLimitMW(route.MaxBodySize))
	}

	// 8.5 bandwidthMW — wrap body + writer (after body limit, before validation)
	if bw := g.bandwidthLimiters.GetLimiter(routeID); bw != nil {
		chain = chain.Use(bandwidthMW(bw))
	}

	// 9. validationMW — request validation (Step 4.6)
	if v := g.validators.GetValidator(routeID); v != nil && v.IsEnabled() {
		chain = chain.Use(validationMW(v))
	}

	// 9.5. graphqlMW — parse, validate depth/complexity, rate limit by operation
	if gql := g.graphqlParsers.GetParser(routeID); gql != nil {
		chain = chain.Use(gql.Middleware())
	}

	// 10. websocketMW — WS upgrade bypass (Step 5)
	if route.WebSocket.Enabled {
		chain = chain.Use(websocketMW(g.wsProxy, func() loadbalancer.Balancer {
			return rp.GetBalancer()
		}))
	}

	// 11. cacheMW — HIT check + store (Steps 6+12)
	if cacheHandler := g.caches.GetHandler(routeID); cacheHandler != nil {
		chain = chain.Use(cacheMW(cacheHandler, g.metricsCollector, routeID))
	}

	// 12. circuitBreakerMW — Allow + Done (Steps 7+11)
	isGRPC := cfg.GRPC.Enabled
	if cb := g.circuitBreakers.GetBreaker(routeID); cb != nil {
		chain = chain.Use(circuitBreakerMW(cb, isGRPC))
	}

	// 13. compressionMW — wrap writer (Step 8)
	if compressor := g.compressors.GetCompressor(routeID); compressor != nil && compressor.IsEnabled() {
		chain = chain.Use(compressionMW(compressor))
	}

	// 14. responseRulesMW — wrap writer + eval (Steps 8.5+10.2)
	hasRespRules := (g.globalRules != nil && g.globalRules.HasResponseRules()) ||
		(routeEngine != nil && routeEngine.HasResponseRules())
	if hasRespRules {
		chain = chain.Use(responseRulesMW(g.globalRules, routeEngine))
	}

	// 15. mirrorMW — buffer + async send + optional comparison (Step 10.5)
	if mirrorHandler := g.mirrors.GetMirror(routeID); mirrorHandler != nil && mirrorHandler.IsEnabled() {
		chain = chain.Use(mirrorMW(mirrorHandler))
	}

	// 15.5 trafficGroupMW — inject A/B variant header + sticky cookie (after mirror, before transforms)
	if wb, ok := rp.GetBalancer().(*loadbalancer.WeightedBalancer); ok && wb.HasStickyPolicy() {
		chain = chain.Use(trafficGroupMW(wb.GetStickyPolicy()))
	}

	// 16. requestTransformMW — headers + body + gRPC (Step 9)
	chain = chain.Use(requestTransformMW(route, g.grpcHandlers[routeID]))

	// 17. responseBodyTransformMW — buffer + replay (Steps 9.5+10.1)
	respBodyCfg := route.Transform.Response.Body
	hasRespBodyTransform := len(respBodyCfg.AddFields) > 0 || len(respBodyCfg.RemoveFields) > 0 || len(respBodyCfg.RenameFields) > 0
	if hasRespBodyTransform {
		chain = chain.Use(responseBodyTransformMW(respBodyCfg))
	}

	// Innermost handler: translator if configured, otherwise the proxy (Step 10)
	var innermost http.Handler = rp
	if translatorHandler := g.translators.GetHandler(routeID); translatorHandler != nil {
		innermost = translatorHandler
	}
	return chain.Handler(innermost)
}

// watchService watches for service changes from registry
func (g *Gateway) watchService(routeID, serviceName string, tags []string) {
	ctx, cancel := context.WithCancel(context.Background())

	g.mu.Lock()
	if existingCancel, ok := g.watchCancels[routeID]; ok {
		existingCancel()
	}
	g.watchCancels[routeID] = cancel
	g.mu.Unlock()

	go func() {
		ch, err := g.registry.Watch(ctx, serviceName)
		if err != nil {
			logging.Error("Failed to watch service",
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

				// Filter by tags if specified
				var filtered []*registry.Service
				for _, svc := range services {
					if hasAllTags(svc.Tags, tags) {
						filtered = append(filtered, svc)
					}
				}

				// Convert to backends
				var backends []*loadbalancer.Backend
				for _, svc := range filtered {
					backends = append(backends, &loadbalancer.Backend{
						URL:     svc.URL(),
						Weight:  1,
						Healthy: svc.Health == registry.HealthPassing,
					})
				}

				// Update route proxy
				g.mu.RLock()
				rp, ok := g.routeProxies[routeID]
				g.mu.RUnlock()

				if ok {
					rp.UpdateBackends(backends)
					logging.Info("Updated backends for route",
						zap.String("route", routeID),
						zap.Int("services", len(backends)),
					)
				}
			}
		}
	}()
}

// hasAllTags checks if service has all required tags
func hasAllTags(serviceTags, requiredTags []string) bool {
	if len(requiredTags) == 0 {
		return true
	}

	tagSet := make(map[string]bool)
	for _, t := range serviceTags {
		tagSet[t] = true
	}

	for _, t := range requiredTags {
		if !tagSet[t] {
			return false
		}
	}
	return true
}

// updateBackendHealth updates backend health status based on health checker
func (g *Gateway) updateBackendHealth(url string, status health.Status) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	healthy := status == health.StatusHealthy

	for _, rp := range g.routeProxies {
		if healthy {
			rp.GetBalancer().MarkHealthy(url)
		} else {
			rp.GetBalancer().MarkUnhealthy(url)
		}
	}
}

// Handler returns the main HTTP handler
func (g *Gateway) Handler() http.Handler {
	chain := middleware.NewBuilder().
		Use(middleware.Recovery()).
		Use(middleware.RequestID()).
		Use(mtls.Middleware())

	if g.tracer != nil {
		chain = chain.Use(g.tracer.Middleware())
	}

	chain = chain.Use(middleware.LoggingWithConfig(middleware.LoggingConfig{
		Format: g.config.Logging.Format,
		JSON:   g.config.Logging.Level == "json",
	}))

	return chain.Handler(http.HandlerFunc(g.serveHTTP))
}

// serveHTTP handles incoming requests by dispatching to the per-route handler pipeline.
func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	match := g.router.Match(r)
	if match == nil {
		errors.ErrNotFound.WriteJSON(w)
		return
	}

	ctx := context.WithValue(r.Context(), routeMatchKey{}, match)
	r = r.WithContext(ctx)

	g.mu.RLock()
	handler, ok := g.routeHandlers[match.Route.ID]
	g.mu.RUnlock()

	if !ok {
		errors.ErrInternalServer.WithDetails("Route handler not found").WriteJSON(w)
		return
	}

	handler.ServeHTTP(w, r)
}

// applyBodyTransform applies body transformations to the request
func applyBodyTransform(r *http.Request, cfg config.BodyTransformConfig) {
	if r.Body == nil {
		return
	}

	ct := r.Header.Get("Content-Type")
	if ct != "application/json" && ct != "application/json; charset=utf-8" {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	r.Body.Close()

	// Parse JSON
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return
	}

	// Add fields
	for key, value := range cfg.AddFields {
		data[key] = value
	}

	// Remove fields
	for _, key := range cfg.RemoveFields {
		delete(data, key)
	}

	// Rename fields
	for oldKey, newKey := range cfg.RenameFields {
		if val, ok := data[oldKey]; ok {
			data[newKey] = val
			delete(data, oldKey)
		}
	}

	newBody, err := json.Marshal(data)
	if err != nil {
		r.Body = io.NopCloser(bytes.NewReader(body))
		return
	}

	r.Body = io.NopCloser(bytes.NewReader(newBody))
	r.ContentLength = int64(len(newBody))
}

// authenticate handles authentication for a request
func (g *Gateway) authenticate(w http.ResponseWriter, r *http.Request, methods []string) bool {
	// If no specific methods, try all available
	if len(methods) == 0 {
		methods = []string{"jwt", "api_key", "oauth"}
	}

	var identity *variables.Identity
	var err error

	for _, method := range methods {
		switch method {
		case "jwt":
			if g.jwtAuth != nil && g.jwtAuth.IsEnabled() {
				identity, err = g.jwtAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		case "api_key":
			if g.apiKeyAuth != nil && g.apiKeyAuth.IsEnabled() {
				identity, err = g.apiKeyAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		case "oauth":
			if g.oauthAuth != nil && g.oauthAuth.IsEnabled() {
				identity, err = g.oauthAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		}

		if identity != nil {
			break
		}
	}

	if identity == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="api", API-Key`)
		errors.ErrUnauthorized.WriteJSON(w)
		return false
	}

	// Add identity to context
	varCtx := variables.GetFromRequest(r)
	varCtx.Identity = identity

	return true
}

// Close closes the gateway and releases resources
func (g *Gateway) Close() error {
	// Cancel all watchers
	g.mu.Lock()
	for _, cancel := range g.watchCancels {
		cancel()
	}
	g.watchCancels = make(map[string]context.CancelFunc)
	g.mu.Unlock()

	// Stop health checker
	g.healthChecker.Stop()

	// Close JWKS providers
	if g.jwtAuth != nil {
		g.jwtAuth.Close()
	}

	// Close tracer
	if g.tracer != nil {
		g.tracer.Close()
	}

	// Close Redis client
	if g.redisClient != nil {
		g.redisClient.Close()
	}

	// Close protocol translators
	g.translators.Close()

	// Close registry
	if g.registry != nil {
		return g.registry.Close()
	}

	return nil
}

// GetRouter returns the router
func (g *Gateway) GetRouter() *router.Router {
	return g.router
}

// GetRegistry returns the registry
func (g *Gateway) GetRegistry() registry.Registry {
	return g.registry
}

// GetHealthChecker returns the health checker
func (g *Gateway) GetHealthChecker() *health.Checker {
	return g.healthChecker
}

// GetCircuitBreakers returns the circuit breaker manager
func (g *Gateway) GetCircuitBreakers() *circuitbreaker.BreakerByRoute {
	return g.circuitBreakers
}

// GetCaches returns the cache manager
func (g *Gateway) GetCaches() *cache.CacheByRoute {
	return g.caches
}

// GetRetryMetrics returns the retry metrics per route
func (g *Gateway) GetRetryMetrics() map[string]*retry.RouteRetryMetrics {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make(map[string]*retry.RouteRetryMetrics)
	for routeID, rp := range g.routeProxies {
		if m := rp.GetRetryMetrics(); m != nil {
			result[routeID] = m
		}
	}
	return result
}

// GetMetricsCollector returns the metrics collector
func (g *Gateway) GetMetricsCollector() *metrics.Collector {
	return g.metricsCollector
}

// GetGlobalRules returns the global rules engine (may be nil).
func (g *Gateway) GetGlobalRules() *rules.RuleEngine {
	return g.globalRules
}

// GetRouteRules returns the per-route rules manager.
func (g *Gateway) GetRouteRules() *rules.RulesByRoute {
	return g.routeRules
}

// GetTranslators returns the protocol translator manager.
func (g *Gateway) GetTranslators() *protocol.TranslatorByRoute {
	return g.translators
}

// GetThrottlers returns the throttle manager.
func (g *Gateway) GetThrottlers() *trafficshape.ThrottleByRoute {
	return g.throttlers
}

// GetBandwidthLimiters returns the bandwidth limiter manager.
func (g *Gateway) GetBandwidthLimiters() *trafficshape.BandwidthByRoute {
	return g.bandwidthLimiters
}

// GetPriorityAdmitter returns the priority admitter (may be nil).
func (g *Gateway) GetPriorityAdmitter() *trafficshape.PriorityAdmitter {
	return g.priorityAdmitter
}

// GetFaultInjectors returns the fault injection manager.
func (g *Gateway) GetFaultInjectors() *trafficshape.FaultInjectionByRoute {
	return g.faultInjectors
}

// GetRateLimiters returns the rate limiter manager.
func (g *Gateway) GetRateLimiters() *ratelimit.RateLimitByRoute {
	return g.rateLimiters
}

// GetTracer returns the tracer (may be nil).
func (g *Gateway) GetTracer() *tracing.Tracer {
	return g.tracer
}

// GetWAFHandlers returns the WAF manager.
func (g *Gateway) GetWAFHandlers() *waf.WAFByRoute {
	return g.wafHandlers
}

// GetMirrors returns the mirror manager.
func (g *Gateway) GetMirrors() *mirror.MirrorByRoute {
	return g.mirrors
}

// GetGraphQLParsers returns the GraphQL parser manager.
func (g *Gateway) GetGraphQLParsers() *graphql.GraphQLByRoute {
	return g.graphqlParsers
}

// GetLoadBalancerInfo returns per-route load balancer algorithm and stats.
func (g *Gateway) GetLoadBalancerInfo() map[string]interface{} {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make(map[string]interface{})
	for _, routeCfg := range g.config.Routes {
		info := map[string]interface{}{
			"algorithm": routeCfg.LoadBalancer,
		}
		if info["algorithm"] == "" {
			if len(routeCfg.TrafficSplit) > 0 {
				info["algorithm"] = "weighted_round_robin"
			} else {
				info["algorithm"] = "round_robin"
			}
		}
		if routeCfg.LoadBalancer == "consistent_hash" {
			info["consistent_hash"] = map[string]interface{}{
				"key":         routeCfg.ConsistentHash.Key,
				"header_name": routeCfg.ConsistentHash.HeaderName,
				"replicas":    routeCfg.ConsistentHash.Replicas,
			}
		}
		if routeCfg.LoadBalancer == "least_response_time" {
			if rp, ok := g.routeProxies[routeCfg.ID]; ok {
				if lrt, ok := rp.GetBalancer().(*loadbalancer.LeastResponseTime); ok {
					info["latencies"] = lrt.GetLatencies()
				}
			}
		}
		result[routeCfg.ID] = info
	}
	return result
}

// GetTrafficSplitStats returns per-route traffic split information.
func (g *Gateway) GetTrafficSplitStats() map[string]interface{} {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make(map[string]interface{})
	for routeID, rp := range g.routeProxies {
		wb, ok := rp.GetBalancer().(*loadbalancer.WeightedBalancer)
		if !ok {
			continue
		}
		groups := wb.GetGroups()
		groupInfos := make([]map[string]interface{}, 0, len(groups))
		for _, g := range groups {
			backends := g.Balancer.GetBackends()
			healthy := 0
			for _, b := range backends {
				if b.Healthy {
					healthy++
				}
			}
			groupInfos = append(groupInfos, map[string]interface{}{
				"name":             g.Name,
				"weight":           g.Weight,
				"backends_total":   len(backends),
				"backends_healthy": healthy,
			})
		}
		info := map[string]interface{}{
			"groups": groupInfos,
			"sticky": wb.HasStickyPolicy(),
		}
		result[routeID] = info
	}
	return result
}

// FeatureStats returns admin stats from features that implement AdminStatsProvider.
func (g *Gateway) FeatureStats() map[string]any {
	result := make(map[string]any)
	for _, f := range g.features {
		if sp, ok := f.(AdminStatsProvider); ok {
			result[f.Name()] = sp.AdminStats()
		}
	}
	return result
}

// GetAPIKeyAuth returns the API key auth for admin API
func (g *Gateway) GetAPIKeyAuth() *auth.APIKeyAuth {
	return g.apiKeyAuth
}

// Stats returns gateway statistics
type Stats struct {
	Routes        int            `json:"routes"`
	HealthyRoutes int            `json:"healthy_routes"`
	Backends      map[string]int `json:"backends"`
}

// GetStats returns current gateway statistics
func (g *Gateway) GetStats() *Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := &Stats{
		Routes:   len(g.routeProxies),
		Backends: make(map[string]int),
	}

	for routeID, rp := range g.routeProxies {
		backends := rp.GetBalancer().GetBackends()
		stats.Backends[routeID] = len(backends)

		healthyCount := 0
		for _, b := range backends {
			if b.Healthy {
				healthyCount++
			}
		}
		if healthyCount > 0 {
			stats.HealthyRoutes++
		}
	}

	return stats
}
