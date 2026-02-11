package gateway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/wudi/gateway/internal/logging"
	"go.uber.org/zap"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/cache"
	"github.com/wudi/gateway/internal/canary"
	"github.com/wudi/gateway/internal/circuitbreaker"
	"github.com/wudi/gateway/internal/coalesce"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/graphql"
	"github.com/wudi/gateway/internal/health"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/loadbalancer/outlier"
	"github.com/wudi/gateway/internal/metrics"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/middleware/accesslog"
	"github.com/wudi/gateway/internal/middleware/altsvc"
	"github.com/wudi/gateway/internal/middleware/auth"
	"github.com/wudi/gateway/internal/middleware/compression"
	"github.com/wudi/gateway/internal/middleware/cors"
	"github.com/wudi/gateway/internal/middleware/decompress"
	"github.com/wudi/gateway/internal/middleware/csrf"
	"github.com/wudi/gateway/internal/middleware/errorpages"
	"github.com/wudi/gateway/internal/middleware/extauth"
	"github.com/wudi/gateway/internal/middleware/geo"
	"github.com/wudi/gateway/internal/middleware/idempotency"
	"github.com/wudi/gateway/internal/middleware/ipfilter"
	"github.com/wudi/gateway/internal/middleware/mtls"
	"github.com/wudi/gateway/internal/middleware/nonce"
	"github.com/wudi/gateway/internal/middleware/maintenance"
	"github.com/wudi/gateway/internal/middleware/realip"
	"github.com/wudi/gateway/internal/middleware/securityheaders"
	"github.com/wudi/gateway/internal/middleware/signing"
	openapivalidation "github.com/wudi/gateway/internal/middleware/openapi"
	"github.com/wudi/gateway/internal/middleware/ratelimit"
	"github.com/wudi/gateway/internal/middleware/timeout"
	"github.com/wudi/gateway/internal/middleware/transform"
	"github.com/wudi/gateway/internal/middleware/validation"
	"github.com/wudi/gateway/internal/middleware/versioning"
	"github.com/wudi/gateway/internal/middleware/waf"
	"github.com/wudi/gateway/internal/mirror"
	"github.com/wudi/gateway/internal/proxy"
	grpcproxy "github.com/wudi/gateway/internal/proxy/grpc"
	"github.com/wudi/gateway/internal/proxy/protocol"
	"github.com/wudi/gateway/internal/registry"
	"github.com/wudi/gateway/internal/registry/consul"
	"github.com/wudi/gateway/internal/registry/etcd"
	"github.com/wudi/gateway/internal/registry/memory"
	"github.com/wudi/gateway/internal/retry"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/internal/rules"
	"github.com/wudi/gateway/internal/tracing"
	"github.com/wudi/gateway/internal/trafficshape"
	"github.com/wudi/gateway/internal/variables"
	"github.com/wudi/gateway/internal/webhook"
	"github.com/wudi/gateway/internal/websocket"
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
	ipFilters        *ipfilter.IPFilterByRoute
	globalIPFilter   *ipfilter.Filter
	corsHandlers     *cors.CORSByRoute
	compressors      *compression.CompressorByRoute
	metricsCollector *metrics.Collector
	validators       *validation.ValidatorByRoute
	mirrors          *mirror.MirrorByRoute
	tracer           *tracing.Tracer

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
	csrfProtectors    *csrf.CSRFByRoute
	outlierDetectors  *outlier.DetectorByRoute
	geoFilters          *geo.GeoByRoute
	geoProvider         geo.Provider
	idempotencyHandlers *idempotency.IdempotencyByRoute
	backendSigners      *signing.SigningByRoute
	decompressors       *decompress.DecompressorByRoute
	securityHeaders     *securityheaders.SecurityHeadersByRoute
	maintenanceHandlers *maintenance.MaintenanceByRoute
	realIPExtractor     *realip.CompiledRealIP
	globalGeo         *geo.CompiledGeo
	webhookDispatcher *webhook.Dispatcher
	http3AltSvcPort   string // port for Alt-Svc header; empty = no HTTP/3

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
		config:            cfg,
		router:            router.New(),
		rateLimiters:      ratelimit.NewRateLimitByRoute(),
		resolver:          variables.NewResolver(),
		circuitBreakers:   circuitbreaker.NewBreakerByRoute(),
		caches:            cache.NewCacheByRoute(nil),
		wsProxy:           websocket.NewProxy(config.WebSocketConfig{}),
		ipFilters:         ipfilter.NewIPFilterByRoute(),
		corsHandlers:      cors.NewCORSByRoute(),
		compressors:       compression.NewCompressorByRoute(),
		metricsCollector:  metrics.NewCollector(),
		validators:        validation.NewValidatorByRoute(),
		mirrors:           mirror.NewMirrorByRoute(),
		grpcHandlers:      make(map[string]*grpcproxy.Handler),
		translators:       protocol.NewTranslatorByRoute(),
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
		csrfProtectors:    csrf.NewCSRFByRoute(),
		outlierDetectors:  outlier.NewDetectorByRoute(),
		geoFilters:          geo.NewGeoByRoute(),
		idempotencyHandlers: idempotency.NewIdempotencyByRoute(),
		backendSigners:      signing.NewSigningByRoute(),
		decompressors:       decompress.NewDecompressorByRoute(),
		securityHeaders:     securityheaders.NewSecurityHeadersByRoute(),
		maintenanceHandlers: maintenance.NewMaintenanceByRoute(),
		routeProxies:        make(map[string]*proxy.RouteProxy),
		routeHandlers:     make(map[string]http.Handler),
		watchCancels:      make(map[string]context.CancelFunc),
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
		&adaptiveConcurrencyFeature{m: g.adaptiveLimiters, global: &cfg.TrafficShaping.AdaptiveConcurrency},
		&wafFeature{g.wafHandlers},
		&graphqlFeature{g.graphqlParsers},
		&coalesceFeature{g.coalescers},
		&canaryFeature{g.canaryControllers},
		&extAuthFeature{g.extAuths},
		&versioningFeature{g.versioners},
		&accessLogFeature{g.accessLogConfigs},
		&openapiFeature{g.openapiValidators},
		&timeoutFeature{g.timeoutConfigs},
		&errorPagesFeature{m: g.errorPages, global: &cfg.ErrorPages},
		&nonceFeature{m: g.nonceCheckers, global: &cfg.Nonce, redis: g.redisClient},
		&csrfFeature{m: g.csrfProtectors, global: &cfg.CSRF},
		&idempotencyFeature{m: g.idempotencyHandlers, global: &cfg.Idempotency, redis: g.redisClient},
		&outlierDetectionFeature{g.outlierDetectors},
		&geoFeature{m: g.geoFilters, global: &cfg.Geo, provider: g.geoProvider},
		&signingFeature{m: g.backendSigners, global: &cfg.BackendSigning},
		&decompressFeature{m: g.decompressors, global: &cfg.RequestDecompression},
		&securityHeadersFeature{m: g.securityHeaders, global: &cfg.SecurityHeaders},
		&maintenanceFeature{m: g.maintenanceHandlers, global: &cfg.Maintenance},
	}

	// Initialize global IP filter
	if cfg.IPFilter.Enabled {
		var err error
		g.globalIPFilter, err = ipfilter.New(cfg.IPFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global IP filter: %w", err)
		}
	}

	// Initialize geo provider and global geo filter
	if cfg.Geo.Enabled && cfg.Geo.Database != "" {
		var err error
		g.geoProvider, err = geo.NewProvider(cfg.Geo.Database)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize geo provider: %w", err)
		}
		g.globalGeo, err = geo.New("_global", cfg.Geo, g.geoProvider)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global geo filter: %w", err)
		}
	}

	// Initialize trusted proxies / real IP extractor
	if len(cfg.TrustedProxies.CIDRs) > 0 {
		var err error
		g.realIPExtractor, err = realip.New(cfg.TrustedProxies.CIDRs, cfg.TrustedProxies.Headers, cfg.TrustedProxies.MaxHops)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize trusted proxies: %w", err)
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
		g.caches.SetRedisClient(g.redisClient)
	}

	// Detect HTTP/3 port for Alt-Svc header
	for _, lc := range cfg.Listeners {
		if lc.Protocol == config.ProtocolHTTP && lc.HTTP.EnableHTTP3 {
			_, port, err := net.SplitHostPort(lc.Address)
			if err == nil {
				g.http3AltSvcPort = port
			}
			break
		}
	}

	// Initialize webhook dispatcher if enabled
	if cfg.Webhooks.Enabled {
		g.webhookDispatcher = webhook.NewDispatcher(cfg.Webhooks)

		// Wire circuit breaker state change callback
		g.circuitBreakers.SetOnStateChange(func(routeID, from, to string) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.CircuitBreakerStateChange, routeID, map[string]interface{}{
				"from": from, "to": to,
			}))
		})

		// Wire canary event callback
		g.canaryControllers.SetOnEvent(func(routeID, eventType string, data map[string]interface{}) {
			g.webhookDispatcher.Emit(webhook.NewEvent(webhook.EventType(eventType), routeID, data))
		})

		// Wire outlier detection callbacks
		g.outlierDetectors.SetCallbacks(
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

	// Initialize health checker
	g.healthChecker = health.NewChecker(health.Config{
		OnChange: func(url string, status health.Status) {
			logging.Info("Backend health changed",
				zap.String("backend", url),
				zap.String("status", string(status)),
			)
			g.updateBackendHealth(url, status)

			// Emit webhook event for backend health changes
			if g.webhookDispatcher != nil {
				var eventType webhook.EventType
				if status == health.StatusHealthy {
					eventType = webhook.BackendHealthy
				} else {
					eventType = webhook.BackendUnhealthy
				}
				g.webhookDispatcher.Emit(webhook.NewEvent(eventType, "", map[string]interface{}{
					"url":    url,
					"status": string(status),
				}))
			}
		},
	})

	// Initialize proxy with transport pool
	pool := g.buildTransportPool(cfg)
	g.proxy = proxy.New(proxy.Config{
		TransportPool: pool,
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

// resolveUpstreamRefs resolves upstream references in a route config by populating
// inline backends from named upstreams. The returned config is a copy with all
// upstream refs resolved to their backend lists. LB settings are also inherited
// from the upstream when the route doesn't specify them.
func resolveUpstreamRefs(cfg *config.Config, routeCfg config.RouteConfig) config.RouteConfig {
	if cfg.Upstreams == nil {
		return routeCfg
	}

	// Resolve route-level upstream
	if routeCfg.Upstream != "" {
		if us, ok := cfg.Upstreams[routeCfg.Upstream]; ok {
			routeCfg.Backends = us.Backends
			routeCfg.Service = us.Service
			if routeCfg.LoadBalancer == "" {
				routeCfg.LoadBalancer = us.LoadBalancer
			}
			if routeCfg.ConsistentHash == (config.ConsistentHashConfig{}) {
				routeCfg.ConsistentHash = us.ConsistentHash
			}
		}
	}

	// Resolve traffic split upstream refs
	for i, split := range routeCfg.TrafficSplit {
		if split.Upstream != "" {
			if us, ok := cfg.Upstreams[split.Upstream]; ok {
				routeCfg.TrafficSplit[i].Backends = us.Backends
			}
		}
	}

	// Resolve versioning upstream refs
	if routeCfg.Versioning.Enabled {
		for ver, vcfg := range routeCfg.Versioning.Versions {
			if vcfg.Upstream != "" {
				if us, ok := cfg.Upstreams[vcfg.Upstream]; ok {
					vcfg.Backends = us.Backends
					routeCfg.Versioning.Versions[ver] = vcfg
				}
			}
		}
	}

	// Resolve mirror upstream ref
	if routeCfg.Mirror.Enabled && routeCfg.Mirror.Upstream != "" {
		if us, ok := cfg.Upstreams[routeCfg.Mirror.Upstream]; ok {
			routeCfg.Mirror.Backends = us.Backends
		}
	}

	return routeCfg
}

// upstreamHealthCheck returns the upstream-level health check config for a backend URL,
// merging global → upstream → per-backend configs.
func upstreamHealthCheck(backendURL string, global config.HealthCheckConfig, upstream *config.HealthCheckConfig, perBackend *config.HealthCheckConfig) health.Backend {
	// Start from global, then apply upstream-level overrides, then per-backend
	merged := global
	if upstream != nil {
		if upstream.Path != "" {
			merged.Path = upstream.Path
		}
		if upstream.Method != "" {
			merged.Method = upstream.Method
		}
		if upstream.Interval > 0 {
			merged.Interval = upstream.Interval
		}
		if upstream.Timeout > 0 {
			merged.Timeout = upstream.Timeout
		}
		if upstream.HealthyAfter > 0 {
			merged.HealthyAfter = upstream.HealthyAfter
		}
		if upstream.UnhealthyAfter > 0 {
			merged.UnhealthyAfter = upstream.UnhealthyAfter
		}
		if len(upstream.ExpectedStatus) > 0 {
			merged.ExpectedStatus = upstream.ExpectedStatus
		}
	}
	return mergeHealthCheckConfig(backendURL, merged, perBackend)
}

// addRoute adds a single route
func (g *Gateway) addRoute(routeCfg config.RouteConfig) error {
	// Resolve upstream references into inline backends/service/LB settings
	routeCfg = resolveUpstreamRefs(g.config, routeCfg)

	// Add route to router
	if err := g.router.AddRoute(routeCfg); err != nil {
		return err
	}

	route := g.router.GetRoute(routeCfg.ID)
	if route == nil {
		return fmt.Errorf("route not found after adding: %s", routeCfg.ID)
	}

	// Set upstream name on route for transport pool resolution
	route.UpstreamName = routeCfg.Upstream

	// Set up backends (skip for echo routes — no backend needed)
	if !routeCfg.Echo {
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
			var usHC *config.HealthCheckConfig
			if routeCfg.Upstream != "" {
				if us, ok := g.config.Upstreams[routeCfg.Upstream]; ok {
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

				// Add to health checker (upstream health check sits between global and per-backend)
				g.healthChecker.AddBackend(upstreamHealthCheck(b.URL, g.config.HealthCheck, usHC, b.HealthCheck))
			}
		}

		// Create route proxy with the appropriate balancer
		g.mu.Lock()
		if routeCfg.Versioning.Enabled {
			versionBackends := make(map[string][]*loadbalancer.Backend)
			for ver, vcfg := range routeCfg.Versioning.Versions {
				var vBacks []*loadbalancer.Backend
				var verUSHC *config.HealthCheckConfig
				if vcfg.Upstream != "" {
					if us, ok := g.config.Upstreams[vcfg.Upstream]; ok {
						verUSHC = us.HealthCheck
					}
				}
				for _, b := range vcfg.Backends {
					weight := b.Weight
					if weight == 0 {
						weight = 1
					}
					vBacks = append(vBacks, &loadbalancer.Backend{URL: b.URL, Weight: weight, Healthy: true})
					g.healthChecker.AddBackend(upstreamHealthCheck(b.URL, g.config.HealthCheck, verUSHC, b.HealthCheck))
				}
				versionBackends[ver] = vBacks
			}
			vb := loadbalancer.NewVersionedBalancer(versionBackends, routeCfg.Versioning.DefaultVersion)
			g.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, vb)
		} else if len(routeCfg.TrafficSplit) > 0 {
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
	}

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
		} else if routeCfg.RateLimit.Algorithm == "sliding_window" {
			g.rateLimiters.AddRouteSlidingWindow(routeCfg.ID, ratelimit.Config{
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

	// Set up protocol translator (replaces RouteProxy as innermost handler; blocked by echo validation)
	if routeCfg.Protocol.Type != "" && g.routeProxies[routeCfg.ID] != nil {
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

	// Override per-try timeout with backend timeout when configured
	if routeCfg.TimeoutPolicy.Backend > 0 && g.routeProxies[routeCfg.ID] != nil {
		g.routeProxies[routeCfg.ID].SetPerTryTimeout(routeCfg.TimeoutPolicy.Backend)
	}

	// Set up canary controller (needs WeightedBalancer, only available after route proxy creation)
	if routeCfg.Canary.Enabled {
		if wb, ok := g.routeProxies[routeCfg.ID].GetBalancer().(*loadbalancer.WeightedBalancer); ok {
			if err := g.canaryControllers.AddRoute(routeCfg.ID, routeCfg.Canary, wb); err != nil {
				return fmt.Errorf("canary: route %s: %w", routeCfg.ID, err)
			}
		}
	}

	// Set up outlier detection (needs Balancer, only available after route proxy creation)
	if routeCfg.OutlierDetection.Enabled {
		g.outlierDetectors.AddRoute(routeCfg.ID, routeCfg.OutlierDetection, g.routeProxies[routeCfg.ID].GetBalancer())
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

	// 1.5. canaryObserverMW — observe traffic group outcomes (only if canary configured)
	if ctrl := g.canaryControllers.GetController(routeID); ctrl != nil {
		chain = chain.Use(canaryObserverMW(ctrl))
	}

	// 2. ipFilterMW — global + per-route (Step 1.1)
	routeIPFilter := g.ipFilters.GetFilter(routeID)
	if g.globalIPFilter != nil || routeIPFilter != nil {
		chain = chain.Use(ipFilterMW(g.globalIPFilter, routeIPFilter))
	}

	// 2.5. geoMW — geo filtering (after IP filter, before CORS)
	routeGeo := g.geoFilters.GetGeo(routeID)
	if g.globalGeo != nil || routeGeo != nil {
		chain = chain.Use(geoMW(g.globalGeo, routeGeo))
	}

	// 2.75. maintenanceMW — return 503 when route is in maintenance mode
	if maint := g.maintenanceHandlers.GetMaintenance(routeID); maint != nil {
		chain = chain.Use(maintenanceMW(maint))
	}

	// 3. corsMW — preflight + headers (Step 1.5)
	if corsHandler := g.corsHandlers.GetHandler(routeID); corsHandler != nil && corsHandler.IsEnabled() {
		chain = chain.Use(corsMW(corsHandler))
	}

	// 4. varContextMW — set RouteID + PathParams (Step 2)
	chain = chain.Use(varContextMW(routeID))

	// 4.05. securityHeadersMW — inject security response headers
	if sh := g.securityHeaders.GetHeaders(routeID); sh != nil {
		chain = chain.Use(securityHeadersMW(sh))
	}

	// 4.1. errorPagesMW — custom error page rendering
	if ep := g.errorPages.GetErrorPages(routeID); ep != nil {
		chain = chain.Use(errorPagesMW(ep))
	}

	// 4.25. accessLogMW — store per-route config + optional body capture
	if alCfg := g.accessLogConfigs.GetConfig(routeID); alCfg != nil {
		chain = chain.Use(accessLogMW(alCfg))
	}

	// 4.5. versioningMW — detect version, strip prefix, deprecation headers
	if ver := g.versioners.GetVersioner(routeID); ver != nil {
		chain = chain.Use(versioningMW(ver))
	}

	// 4.75. timeoutMW — request-level context deadline + Retry-After on 504
	if ct := g.timeoutConfigs.GetTimeout(routeID); ct != nil {
		chain = chain.Use(timeoutMW(ct))
	}

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

	// 6.25 extAuthMW — external auth service (after built-in auth, before priority)
	if ea := g.extAuths.GetAuth(routeID); ea != nil {
		chain = chain.Use(extAuthMW(ea))
	}

	// 6.3 nonceMW — replay prevention (after auth, needs Identity for per_client scope)
	if nc := g.nonceCheckers.GetChecker(routeID); nc != nil {
		chain = chain.Use(nonceMW(nc))
	}

	// 6.35 csrfMW — CSRF protection (after nonce, before priority)
	if cp := g.csrfProtectors.GetProtector(routeID); cp != nil {
		chain = chain.Use(csrfMW(cp))
	}

	// 6.4 idempotencyMW — replay cached responses for duplicate idempotency keys
	if ih := g.idempotencyHandlers.GetHandler(routeID); ih != nil {
		chain = chain.Use(idempotencyMW(ih))
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

	// 8.25 requestDecompressMW — decompress Content-Encoding (after body limit, before bandwidth)
	if d := g.decompressors.GetDecompressor(routeID); d != nil && d.IsEnabled() {
		chain = chain.Use(requestDecompressMW(d))
	}

	// 8.5 bandwidthMW — wrap body + writer (after body limit, before validation)
	if bw := g.bandwidthLimiters.GetLimiter(routeID); bw != nil {
		chain = chain.Use(bandwidthMW(bw))
	}

	// 9. validationMW — request validation (Step 4.6)
	if v := g.validators.GetValidator(routeID); v != nil && v.IsEnabled() {
		chain = chain.Use(validationMW(v))
	}

	// 9.1. openapiRequestMW — OpenAPI request validation
	if ov := g.openapiValidators.GetValidator(routeID); ov != nil {
		chain = chain.Use(openapiRequestMW(ov))
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

	// 11.5. coalesceMW — singleflight dedup (between cache and circuit breaker)
	if c := g.coalescers.GetCoalescer(routeID); c != nil {
		chain = chain.Use(coalesceMW(c))
	}

	// 12. circuitBreakerMW — Allow + Done (Steps 7+11)
	isGRPC := cfg.GRPC.Enabled
	if cb := g.circuitBreakers.GetBreaker(routeID); cb != nil {
		chain = chain.Use(circuitBreakerMW(cb, isGRPC))
	}

	// 12.25. outlierDetectionMW — per-backend passive metrics
	if det := g.outlierDetectors.GetDetector(routeID); det != nil {
		chain = chain.Use(outlierDetectionMW(det))
	}

	// 12.5. adaptiveConcurrencyMW — AIMD concurrency limiting
	if al := g.adaptiveLimiters.GetLimiter(routeID); al != nil {
		chain = chain.Use(adaptiveConcurrencyMW(al))
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
	if rp != nil {
		if wb, ok := rp.GetBalancer().(*loadbalancer.WeightedBalancer); ok && wb.HasStickyPolicy() {
			chain = chain.Use(trafficGroupMW(wb.GetStickyPolicy()))
		}
	}

	// Compile body transforms once
	var reqBodyTransform *transform.CompiledBodyTransform
	if route.Transform.Request.Body.IsActive() {
		reqBodyTransform, _ = transform.NewCompiledBodyTransform(route.Transform.Request.Body)
	}
	var respBodyTransform *transform.CompiledBodyTransform
	if route.Transform.Response.Body.IsActive() {
		respBodyTransform, _ = transform.NewCompiledBodyTransform(route.Transform.Response.Body)
	}

	// 16. requestTransformMW — headers + body + gRPC (Step 9)
	chain = chain.Use(requestTransformMW(route, g.grpcHandlers[routeID], reqBodyTransform))

	// 16.5. backendSigningMW — HMAC sign the final request
	if signer := g.backendSigners.GetSigner(routeID); signer != nil {
		chain = chain.Use(backendSigningMW(signer))
	}

	// 17. responseBodyTransformMW — buffer + replay (Steps 9.5+10.1)
	if respBodyTransform != nil {
		chain = chain.Use(transform.ResponseBodyTransformMiddleware(respBodyTransform))
	}

	// Innermost handler: echo, translator, or proxy (Step 10)
	var innermost http.Handler
	if cfg.Echo {
		innermost = proxy.NewEchoHandler(routeID)
	} else if translatorHandler := g.translators.GetHandler(routeID); translatorHandler != nil {
		innermost = translatorHandler
	} else {
		innermost = rp
	}

	// 17.5 responseValidationMW — validate raw backend response (closest to proxy)
	respValidator := g.validators.GetValidator(routeID)
	openapiV := g.openapiValidators.GetValidator(routeID)
	hasRespValidation := (respValidator != nil && respValidator.HasResponseSchema()) ||
		(openapiV != nil && openapiV.ValidatesResponse())
	if hasRespValidation {
		innermost = responseValidationMW(respValidator, openapiV)(innermost)
	}

	return chain.Handler(innermost)
}

// mergeHealthCheckConfig builds a health.Backend from global and per-backend config.
func mergeHealthCheckConfig(backendURL string, global config.HealthCheckConfig, perBackend *config.HealthCheckConfig) health.Backend {
	b := health.Backend{URL: backendURL}

	// Start from global
	b.HealthPath = global.Path
	b.Method = global.Method
	b.Interval = global.Interval
	b.Timeout = global.Timeout
	b.HealthyAfter = global.HealthyAfter
	b.UnhealthyAfter = global.UnhealthyAfter

	// Parse global expected status
	for _, s := range global.ExpectedStatus {
		if r, err := health.ParseStatusRange(s); err == nil {
			b.ExpectedStatus = append(b.ExpectedStatus, r)
		}
	}

	// Override with per-backend values where non-zero/non-empty
	if perBackend != nil {
		if perBackend.Path != "" {
			b.HealthPath = perBackend.Path
		}
		if perBackend.Method != "" {
			b.Method = perBackend.Method
		}
		if perBackend.Interval > 0 {
			b.Interval = perBackend.Interval
		}
		if perBackend.Timeout > 0 {
			b.Timeout = perBackend.Timeout
		}
		if perBackend.HealthyAfter > 0 {
			b.HealthyAfter = perBackend.HealthyAfter
		}
		if perBackend.UnhealthyAfter > 0 {
			b.UnhealthyAfter = perBackend.UnhealthyAfter
		}
		if len(perBackend.ExpectedStatus) > 0 {
			b.ExpectedStatus = nil
			for _, s := range perBackend.ExpectedStatus {
				if r, err := health.ParseStatusRange(s); err == nil {
					b.ExpectedStatus = append(b.ExpectedStatus, r)
				}
			}
		}
	}

	return b
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
		Use(middleware.Recovery())

	// Real IP extraction from trusted proxies (before everything else)
	if g.realIPExtractor != nil {
		chain = chain.Use(g.realIPExtractor.Middleware)
	}

	chain = chain.Use(middleware.RequestID())

	// Alt-Svc: advertise HTTP/3 on HTTP/1+2 responses
	if g.http3AltSvcPort != "" {
		chain = chain.Use(altsvc.Middleware(g.http3AltSvcPort))
	}

	chain = chain.Use(mtls.Middleware())

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

	// Close webhook dispatcher
	if g.webhookDispatcher != nil {
		g.webhookDispatcher.Close()
	}

	// Stop canary controllers
	g.canaryControllers.StopAll()

	// Stop adaptive concurrency limiters
	g.adaptiveLimiters.StopAll()

	// Stop outlier detectors
	g.outlierDetectors.StopAll()

	// Close nonce checkers
	g.nonceCheckers.CloseAll()

	// Close idempotency handlers
	g.idempotencyHandlers.CloseAll()

	// Close ext auth clients
	g.extAuths.CloseAll()

	// Close geo provider
	if g.geoProvider != nil {
		g.geoProvider.Close()
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

// GetCoalescers returns the coalesce manager.
func (g *Gateway) GetCoalescers() *coalesce.CoalesceByRoute {
	return g.coalescers
}

// GetCanaryControllers returns the canary controller manager.
func (g *Gateway) GetCanaryControllers() *canary.CanaryByRoute {
	return g.canaryControllers
}

// GetAdaptiveLimiters returns the adaptive concurrency limiter manager.
func (g *Gateway) GetAdaptiveLimiters() *trafficshape.AdaptiveConcurrencyByRoute {
	return g.adaptiveLimiters
}

// GetExtAuths returns the ext auth manager.
func (g *Gateway) GetExtAuths() *extauth.ExtAuthByRoute {
	return g.extAuths
}

// GetVersioners returns the versioning manager.
func (g *Gateway) GetVersioners() *versioning.VersioningByRoute {
	return g.versioners
}

// GetAccessLogConfigs returns the access log config manager.
func (g *Gateway) GetAccessLogConfigs() *accesslog.AccessLogByRoute {
	return g.accessLogConfigs
}

// GetOpenAPIValidators returns the OpenAPI validator manager.
func (g *Gateway) GetOpenAPIValidators() *openapivalidation.OpenAPIByRoute {
	return g.openapiValidators
}

// GetTimeoutConfigs returns the timeout config manager.
func (g *Gateway) GetTimeoutConfigs() *timeout.TimeoutByRoute {
	return g.timeoutConfigs
}

// GetErrorPages returns the error pages manager.
func (g *Gateway) GetErrorPages() *errorpages.ErrorPagesByRoute {
	return g.errorPages
}

// GetNonceCheckers returns the nonce checker manager.
func (g *Gateway) GetNonceCheckers() *nonce.NonceByRoute {
	return g.nonceCheckers
}

// GetCSRFProtectors returns the CSRF protection manager.
func (g *Gateway) GetCSRFProtectors() *csrf.CSRFByRoute {
	return g.csrfProtectors
}

// GetOutlierDetectors returns the outlier detection manager.
func (g *Gateway) GetOutlierDetectors() *outlier.DetectorByRoute {
	return g.outlierDetectors
}

// GetIdempotencyHandlers returns the idempotency handler manager.
func (g *Gateway) GetIdempotencyHandlers() *idempotency.IdempotencyByRoute {
	return g.idempotencyHandlers
}

// GetBackendSigners returns the backend signing manager.
func (g *Gateway) GetBackendSigners() *signing.SigningByRoute {
	return g.backendSigners
}

// GetGeoFilters returns the geo filter manager.
func (g *Gateway) GetGeoFilters() *geo.GeoByRoute {
	return g.geoFilters
}

// GetCompressors returns the compression manager.
func (g *Gateway) GetCompressors() *compression.CompressorByRoute {
	return g.compressors
}

// GetDecompressors returns the request decompression manager.
func (g *Gateway) GetDecompressors() *decompress.DecompressorByRoute {
	return g.decompressors
}

// GetMaintenanceHandlers returns the maintenance ByRoute manager.
func (g *Gateway) GetMaintenanceHandlers() *maintenance.MaintenanceByRoute {
	return g.maintenanceHandlers
}

// GetSecurityHeaders returns the security headers ByRoute manager.
func (g *Gateway) GetSecurityHeaders() *securityheaders.SecurityHeadersByRoute {
	return g.securityHeaders
}

// GetRealIPExtractor returns the real IP extractor (may be nil).
func (g *Gateway) GetRealIPExtractor() *realip.CompiledRealIP {
	return g.realIPExtractor
}

// GetWebhookDispatcher returns the webhook dispatcher (may be nil).
func (g *Gateway) GetWebhookDispatcher() *webhook.Dispatcher {
	return g.webhookDispatcher
}

// GetUpstreams returns the configured upstream map.
func (g *Gateway) GetUpstreams() map[string]config.UpstreamConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.config.Upstreams
}

// GetTransportPool returns the proxy's transport pool.
func (g *Gateway) GetTransportPool() *proxy.TransportPool {
	return g.proxy.GetTransportPool()
}

// buildTransportPool constructs a TransportPool from the config.
// Three-level merge: defaults → global transport → per-upstream transport.
func (g *Gateway) buildTransportPool(cfg *config.Config) *proxy.TransportPool {
	// Start from defaults, apply global config
	baseCfg := proxy.MergeTransportConfigs(proxy.DefaultTransportConfig, cfg.Transport)

	// Apply DNS resolver if configured
	if len(cfg.DNSResolver.Nameservers) > 0 {
		baseCfg.Resolver = proxy.NewResolver(cfg.DNSResolver.Nameservers, cfg.DNSResolver.Timeout)
	}

	pool := proxy.NewTransportPoolWithDefault(baseCfg)

	// Create per-upstream transports
	for name, us := range cfg.Upstreams {
		if us.Transport == (config.TransportConfig{}) {
			continue // no per-upstream overrides
		}
		usCfg := proxy.MergeTransportConfigs(baseCfg, us.Transport)
		pool.Set(name, usCfg)
	}

	return pool
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
