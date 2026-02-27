package runway

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/internal/logging"
	"go.uber.org/zap"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/abtest"
	"github.com/wudi/runway/internal/bluegreen"
	"github.com/wudi/runway/internal/cache"
	"github.com/wudi/runway/internal/canary"
	"github.com/wudi/runway/internal/catalog"
	"github.com/wudi/runway/internal/circuitbreaker"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/graphql"
	"github.com/wudi/runway/internal/health"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/metrics"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/middleware/ai"
	"github.com/wudi/runway/internal/middleware/allowedhosts"
	"github.com/wudi/runway/internal/middleware/altsvc"
	"github.com/wudi/runway/internal/middleware/auditlog"
	"github.com/wudi/runway/internal/middleware/auth"
	"github.com/wudi/runway/internal/middleware/backpressure"
	"github.com/wudi/runway/internal/middleware/debug"
	"github.com/wudi/runway/internal/middleware/errorpages"
	"github.com/wudi/runway/internal/middleware/extauth"
	"github.com/wudi/runway/internal/middleware/httpsredirect"
	"github.com/wudi/runway/internal/middleware/idempotency"
	"github.com/wudi/runway/internal/middleware/loadshed"
	"github.com/wudi/runway/internal/middleware/luascript"
	"github.com/wudi/runway/internal/middleware/maintenance"
	"github.com/wudi/runway/internal/middleware/mtls"
	"github.com/wudi/runway/internal/middleware/nonce"
	openapivalidation "github.com/wudi/runway/internal/middleware/openapi"
	"github.com/wudi/runway/internal/middleware/ratelimit"
	"github.com/wudi/runway/internal/middleware/requestqueue"
	"github.com/wudi/runway/internal/middleware/serviceratelimit"
	"github.com/wudi/runway/internal/middleware/sse"
	"github.com/wudi/runway/internal/middleware/ssrf"
	"github.com/wudi/runway/internal/middleware/tokenrevoke"
	"github.com/wudi/runway/internal/middleware/transform"
	wasmPlugin "github.com/wudi/runway/internal/middleware/wasm"
	"github.com/wudi/runway/internal/mirror"
	"github.com/wudi/runway/internal/proxy"
	"github.com/wudi/runway/internal/proxy/protocol"
	_ "github.com/wudi/runway/internal/proxy/protocol/graphql"
	_ "github.com/wudi/runway/internal/proxy/protocol/soap"
	"github.com/wudi/runway/internal/registry"
	"github.com/wudi/runway/internal/registry/consul"
	dnsregistry "github.com/wudi/runway/internal/registry/dns"
	"github.com/wudi/runway/internal/registry/etcd"
	"github.com/wudi/runway/internal/registry/memory"
	"github.com/wudi/runway/internal/retry"
	"github.com/wudi/runway/internal/router"
	"github.com/wudi/runway/internal/rules"
	"github.com/wudi/runway/internal/schemaevolution"
	"github.com/wudi/runway/internal/tracing"
	"github.com/wudi/runway/internal/trafficreplay"
	"github.com/wudi/runway/internal/trafficshape"
	"github.com/wudi/runway/internal/webhook"
	"github.com/wudi/runway/internal/websocket"
	"github.com/wudi/runway/variables"
)

// Runway is the main API gateway
type Runway struct {
	// Hot path — read on every request via serveHTTP (own cache line)
	routeHandlers atomic.Pointer[map[string]http.Handler]
	routeProxies  atomic.Pointer[map[string]*proxy.RouteProxy]
	_pad          [64 - 2*8]byte // prevent false sharing with cold fields below

	config        *config.Config
	router        *router.Router
	proxy         *proxy.Proxy
	registry      registry.Registry
	healthChecker *health.Checker
	resolver      *variables.Resolver

	// All per-route and per-reload managers (embedded for transparent field access)
	routeManagers

	// Shared infrastructure (persists across reloads)
	wsProxy           *websocket.Proxy
	metricsCollector  *metrics.Collector
	tracer            *tracing.Tracer
	redisClient       *redis.Client // shared Redis client for distributed features
	webhookDispatcher *webhook.Dispatcher
	catalogBuilder    *catalog.Builder
	schemaChecker     *schemaevolution.Checker

	// Global singletons rebuilt inline during Reload (not in routeManagers)
	serviceLimiter  *serviceratelimit.ServiceLimiter
	debugHandler    *debug.Handler
	httpsRedirect   *httpsredirect.CompiledHTTPSRedirect
	allowedHosts    *allowedhosts.CompiledAllowedHosts
	ssrfDialer      *ssrf.SafeDialer
	http3AltSvcPort string // port for Alt-Svc header; empty = no HTTP/3
	loadShedder     *loadshed.LoadShedder

	features      []Feature
	adminFeatures []Feature // Runway-level stats features, set once, never swapped on reload

	// External customization (from public gateway.RunwayBuilder)
	customSlots       []CustomSlot
	customGlobalSlots []CustomGlobalSlot
	externalFeatures  []ExternalFeature

	watchCancels map[string]context.CancelFunc
	mu           sync.RWMutex // cold: only held during route add/reload
}

// storeAtomicMap atomically stores an entry in a copy-on-write map behind an atomic.Pointer.
func storeAtomicMap[T any](p *atomic.Pointer[map[string]T], id string, item T) {
	old := *p.Load()
	m := make(map[string]T, len(old)+1)
	for k, v := range old {
		m[k] = v
	}
	m[id] = item
	p.Store(&m)
}

// statusRecorder wraps http.ResponseWriter to capture the status code
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

var statusRecorderPool = sync.Pool{
	New: func() any {
		return &statusRecorder{}
	},
}

func getStatusRecorder(w http.ResponseWriter) *statusRecorder {
	rec := statusRecorderPool.Get().(*statusRecorder)
	rec.ResponseWriter = w
	rec.statusCode = 200
	return rec
}

func putStatusRecorder(rec *statusRecorder) {
	rec.ResponseWriter = nil
	statusRecorderPool.Put(rec)
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
func New(cfg *config.Config) (*Runway, error) {
	g := &Runway{
		config:           cfg,
		router:           router.New(),
		resolver:         variables.NewResolver(),
		wsProxy:          websocket.NewProxy(config.WebSocketConfig{}),
		metricsCollector: metrics.NewCollector(),
		routeManagers:    newRouteManagers(cfg, nil),
		watchCancels:     make(map[string]context.CancelFunc),
	}

	// Initialize atomic pointers for hot-path map access
	rp := make(map[string]*proxy.RouteProxy)
	g.routeProxies.Store(&rp)
	rh := make(map[string]http.Handler)
	g.routeHandlers.Store(&rh)

	// Initialize load shedder if enabled
	if cfg.LoadShedding.Enabled {
		g.loadShedder = loadshed.New(cfg.LoadShedding)
	}

	// Initialize tracer
	if cfg.Tracing.Enabled {
		var err error
		g.tracer, err = tracing.New(cfg.Tracing)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize tracer: %w", err)
		}
	}

	// Initialize Redis client if configured (before initGlobals/buildFeatures which may use it)
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

	// Initialize global singletons (shared between New and Reload)
	if err := g.routeManagers.initGlobals(cfg, g.redisClient); err != nil {
		return nil, err
	}

	// Register per-route features (shared between New and Reload)
	g.features = buildFeatures(&g.routeManagers, cfg, g.redisClient)

	// Register admin-only features (Runway-level stats, set once, never swapped on reload)
	g.adminFeatures = []Feature{
		noOpFeature("retries", "/retries", func() []string { return nil }, func() any {
			result := make(map[string]interface{})
			for routeID, rp := range *g.routeProxies.Load() {
				if m := rp.GetRetryMetrics(); m != nil {
					result[routeID] = m.Snapshot()
				}
			}
			if len(result) == 0 {
				return nil
			}
			return result
		}),
		noOpFeature("session_affinity", "/session-affinity", func() []string {
			var ids []string
			for routeID, rp := range *g.routeProxies.Load() {
				if _, ok := rp.GetBalancer().(*loadbalancer.SessionAffinityBalancer); ok {
					ids = append(ids, routeID)
				}
			}
			return ids
		}, func() any {
			result := make(map[string]interface{})
			for routeID, rp := range *g.routeProxies.Load() {
				if sa, ok := rp.GetBalancer().(*loadbalancer.SessionAffinityBalancer); ok {
					result[routeID] = map[string]interface{}{
						"cookie_name": sa.CookieName(),
						"ttl":         sa.TTL().String(),
					}
				}
			}
			if len(result) == 0 {
				return nil
			}
			return result
		}),
		noOpFeature("traffic_splits", "/traffic-splits", func() []string { return nil }, func() any {
			return g.GetTrafficSplitStats()
		}),
		noOpFeature("follow_redirects", "/follow-redirects", func() []string { return nil }, func() any {
			return g.GetFollowRedirectStats()
		}),
		noOpFeature("tracing", "/tracing", func() []string { return nil }, func() any {
			if g.tracer == nil {
				return map[string]interface{}{"enabled": false}
			}
			return g.tracer.Status()
		}),
		noOpFeature("service_rate_limit", "/service-rate-limit", func() []string { return nil }, func() any {
			if g.serviceLimiter == nil {
				return map[string]interface{}{"enabled": false}
			}
			return g.serviceLimiter.Stats()
		}),
		noOpFeature("webhooks", "/webhooks", func() []string { return nil }, func() any {
			if g.webhookDispatcher == nil {
				return map[string]interface{}{"enabled": false}
			}
			return g.webhookDispatcher.Stats()
		}),
	}

	// Initialize HTTPS redirect
	if cfg.HTTPSRedirect.Enabled {
		g.httpsRedirect = httpsredirect.New(cfg.HTTPSRedirect)
	}

	// Initialize allowed hosts
	if cfg.AllowedHosts.Enabled {
		g.allowedHosts = allowedhosts.New(cfg.AllowedHosts)
	}

	// Initialize service-level rate limiter
	if cfg.ServiceRateLimit.Enabled {
		g.serviceLimiter = serviceratelimit.New(cfg.ServiceRateLimit)
	}

	// Initialize debug endpoint
	if cfg.DebugEndpoint.Enabled {
		g.debugHandler = debug.New(cfg.DebugEndpoint, cfg)
	}

	// Initialize SSRF protection dialer (for admin stats)
	if cfg.SSRFProtection.Enabled {
		dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		if sd, err := ssrf.New(dialer, cfg.SSRFProtection); err == nil {
			g.ssrfDialer = sd
		}
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
		g.routeManagers.wireWebhookCallbacks(g.webhookDispatcher)
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

	// Initialize authentication (shared between New and Reload)
	if err := g.routeManagers.initAuth(cfg); err != nil {
		return nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Initialize routes
	if err := g.initRoutes(); err != nil {
		return nil, fmt.Errorf("failed to initialize routes: %w", err)
	}

	// Initialize API catalog
	if cfg.Admin.Catalog.Enabled {
		g.catalogBuilder = catalog.NewBuilder(
			cfg.Admin.Catalog,
			cfg.Routes,
			g.router.GetRoutes,
			g.openapiValidators,
		)
	}

	// Initialize schema evolution checker
	if cfg.OpenAPI.SchemaEvolution.Enabled {
		checker, err := schemaevolution.NewChecker(cfg.OpenAPI.SchemaEvolution, logging.Global())
		if err != nil {
			return nil, fmt.Errorf("failed to initialize schema evolution checker: %w", err)
		}
		g.schemaChecker = checker

		// Store initial spec versions (no comparison on first load)
		for _, specCfg := range cfg.OpenAPI.Specs {
			if specCfg.File != "" {
				doc, loadErr := openapivalidation.LoadSpec(specCfg.File)
				if loadErr == nil {
					checker.CheckAndStore(specCfg.ID, doc)
				}
			}
		}
		// Also store per-route specs
		for _, rc := range cfg.Routes {
			if rc.OpenAPI.SpecFile != "" {
				doc, loadErr := openapivalidation.LoadSpec(rc.OpenAPI.SpecFile)
				if loadErr == nil {
					specID := rc.OpenAPI.SpecFile
					if rc.OpenAPI.SpecID != "" {
						specID = rc.OpenAPI.SpecID
					}
					checker.CheckAndStore(specID, doc)
				}
			}
		}
	}

	return g, nil
}

// NewWithOptions creates a new Runway with external customizations from the
// public gateway.RunwayBuilder. If opts.UseDefaults is true (the common case),
// all built-in features are registered as usual. Custom slots and features are
// stored for use during buildRouteHandler and Handler.
func NewWithOptions(cfg *config.Config, opts ExternalOptions) (*Runway, error) {
	g, err := New(cfg)
	if err != nil {
		return nil, err
	}

	g.customSlots = opts.CustomSlots
	g.customGlobalSlots = opts.CustomGlobal
	g.externalFeatures = opts.ExternalFeatures

	// Run Setup on external features for already-initialized routes
	// (routes were initialized in New via initRoutes, so we re-run external
	// feature setup for each existing route)
	for _, ef := range g.externalFeatures {
		for _, rc := range cfg.Routes {
			if err := ef.Feature.Setup(rc.ID, rc); err != nil {
				return nil, fmt.Errorf("external feature %q setup for route %q: %w",
					ef.Feature.Name(), rc.ID, err)
			}
		}
	}

	// Rebuild route handlers to include custom middleware slots
	if len(g.customSlots) > 0 || len(g.externalFeatures) > 0 {
		g.rebuildAllRouteHandlers(cfg)
	}

	return g, nil
}

// rebuildAllRouteHandlers regenerates all route handlers to incorporate
// custom middleware slots.
func (g *Runway) rebuildAllRouteHandlers(cfg *config.Config) {
	handlers := make(map[string]http.Handler)
	proxyMap := *g.routeProxies.Load()

	for _, rc := range cfg.Routes {
		rp, ok := proxyMap[rc.ID]
		if !ok {
			continue
		}
		route := g.router.GetRoute(rc.ID)
		if route == nil {
			continue
		}
		handlers[rc.ID] = g.buildRouteHandler(&g.routeManagers, rc.ID, rc, route, rp)
	}

	// Merge with existing handlers (non-route handlers stay)
	existing := *g.routeHandlers.Load()
	merged := make(map[string]http.Handler, len(existing))
	for k, v := range existing {
		merged[k] = v
	}
	for k, v := range handlers {
		merged[k] = v
	}
	g.routeHandlers.Store(&merged)
}

// initRegistry initializes the service registry
func (g *Runway) initRegistry() error {
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
	case "dns":
		g.registry, err = dnsregistry.New(g.config.Registry.DNSSRV)
	default:
		g.registry = memory.New()
	}

	return err
}

// allFeatures returns all features (per-route + admin-only) for admin API iteration.
func (g *Runway) allFeatures() []Feature {
	if len(g.adminFeatures) == 0 {
		return g.features
	}
	all := make([]Feature, 0, len(g.features)+len(g.adminFeatures))
	all = append(all, g.features...)
	all = append(all, g.adminFeatures...)
	return all
}

// initRoutes initializes all routes from configuration
func (g *Runway) initRoutes() error {
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

// addRoute adds a single route during initial startup.
func (g *Runway) addRoute(routeCfg config.RouteConfig) error {
	return g.setupRoute(&routeSetup{
		cfg:             g.config,
		rtr:             g.router,
		rm:              &g.routeManagers,
		features:        g.features,
		registerBackend: g.healthChecker.AddBackend,
		watchService:    g.watchService,
		storeProxy: func(id string, rp *proxy.RouteProxy) { storeAtomicMap(&g.routeProxies, id, rp) },
		buildHandler: func(routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler {
			return g.buildRouteHandler(&g.routeManagers, routeID, cfg, route, rp)
		},
		storeHandler: func(id string, h http.Handler) { storeAtomicMap(&g.routeHandlers, id, h) },
	}, routeCfg)
}

// createBalancerForBackends creates a load balancer for the given backend set using route LB config.
func createBalancerForBackends(cfg config.RouteConfig, backends []*loadbalancer.Backend) loadbalancer.Balancer {
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

// slot creates a named middleware slot that looks up a per-route object and calls Middleware().
// Set skipBody=true to skip for passthrough routes, and skipFlag!=0 to wrap with skipFlagMW.
func slot[T interface{ Middleware() middleware.Middleware }](name string, skipBody bool, skipFlag variables.SkipFlags, mgr *byroute.Manager[T], routeID string) namedSlot {
	if skipBody {
		return namedSlot{name, func() middleware.Middleware { return nil }}
	}
	return namedSlot{name, func() middleware.Middleware {
		if x, ok := mgr.Get(routeID); ok {
			if mw := x.Middleware(); skipFlag != 0 {
				return skipFlagMW(skipFlag, mw)
			} else {
				return mw
			}
		}
		return nil
	}}
}

// enabledSlot is like slot but also checks IsEnabled() before calling Middleware().
func enabledSlot[T interface {
	Middleware() middleware.Middleware
	IsEnabled() bool
}](name string, skipBody bool, skipFlag variables.SkipFlags, mgr *byroute.Manager[T], routeID string) namedSlot {
	if skipBody {
		return namedSlot{name, func() middleware.Middleware { return nil }}
	}
	return namedSlot{name, func() middleware.Middleware {
		if x, ok := mgr.Get(routeID); ok && x.IsEnabled() {
			if mw := x.Middleware(); skipFlag != 0 {
				return skipFlagMW(skipFlag, mw)
			} else {
				return mw
			}
		}
		return nil
	}}
}

// methodSlot creates a named slot using a custom function to get the middleware.
func methodSlot[T any](name string, mgr *byroute.Manager[T], routeID string, fn func(T) middleware.Middleware) namedSlot {
	return namedSlot{name, func() middleware.Middleware {
		if x, ok := mgr.Get(routeID); ok {
			return fn(x)
		}
		return nil
	}}
}

// buildRouteHandler constructs the per-route middleware pipeline.
// Chain ordering matches CLAUDE.md serveHTTP flow exactly.
func (g *Runway) buildRouteHandler(rm *routeManagers, routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler {
	skipBody := cfg.Passthrough
	isGRPC := cfg.GRPC.Enabled
	routeEngine := rm.routeRules.Lookup(routeID)

	// Pre-compile body transforms (skip for passthrough routes)
	var reqBodyTransform, respBodyTransform *transform.CompiledBodyTransform
	if !skipBody && route.Transform.Request.Body.IsActive() {
		reqBodyTransform, _ = transform.NewCompiledBodyTransform(route.Transform.Request.Body)
	}
	if !skipBody && route.Transform.Response.Body.IsActive() {
		respBodyTransform, _ = transform.NewCompiledBodyTransform(route.Transform.Response.Body)
	}

	// Middleware chain in order. Each slot has a name (for anchor-based insertion)
	// and a build function that returns a middleware or nil to skip.
	// Order matches CLAUDE.md serveHTTP flow exactly — do not reorder.
	slots := []namedSlot{
		{"metrics", func() middleware.Middleware { return metricsMW(g.metricsCollector, routeID) }},
		slot("slo", false, 0, &rm.sloTrackers.Manager, routeID),
		{"canary_observer", func() middleware.Middleware {
			if ctrl := rm.canaryControllers.Lookup(routeID); ctrl != nil {
				return trafficObserverMW(ctrl)
			}
			if bg := rm.blueGreenControllers.Lookup(routeID); bg != nil {
				return trafficObserverMW(bg)
			}
			if ab := rm.abTests.Lookup(routeID); ab != nil {
				return trafficObserverMW(ab)
			}
			return nil
		}},
		{"ip_filter", func() middleware.Middleware {
			rf := rm.ipFilters.Lookup(routeID)
			if rm.globalIPFilter != nil || rf != nil {
				return ipFilterMW(rm.globalIPFilter, rf)
			}
			return nil
		}},
		{"geo", func() middleware.Middleware {
			rg := rm.geoFilters.Lookup(routeID)
			if rm.globalGeo != nil || rg != nil {
				return geoMW(rm.globalGeo, rg)
			}
			return nil
		}},
		slot("maintenance", false, 0, &rm.maintenanceHandlers.Manager, routeID),
		slot("bot_detection", false, 0, &rm.botDetectors.Manager, routeID),
		slot("ai_crawl_control", false, 0, &rm.aiCrawlControllers.Manager, routeID),
		{"ip_blocklist", func() middleware.Middleware {
			bl := rm.ipBlocklists.Lookup(routeID)
			if rm.globalBlocklist != nil || bl != nil {
				return ipBlocklistMW(rm.globalBlocklist, bl)
			}
			return nil
		}},
		slot("client_mtls", false, 0, &rm.clientMTLSVerifiers.Manager, routeID),
		enabledSlot("cors", false, 0, &rm.corsHandlers.Manager, routeID),
		{"var_context", func() middleware.Middleware { return varContextMW(routeID) }},
		slot("security_headers", false, 0, &rm.securityHeaders.Manager, routeID),
		slot("cdn_headers", false, 0, &rm.cdnHeaders.Manager, routeID),
		slot("edge_cache_rules", false, 0, &rm.edgeCacheRules.Manager, routeID),
		slot("error_pages", false, 0, &rm.errorPages.Manager, routeID),
		{"access_log", func() middleware.Middleware {
			if al := rm.accessLogConfigs.Lookup(routeID); al != nil {
				return skipFlagMW(variables.SkipAccessLog, al.Middleware())
			}
			return nil
		}},
		slot("audit_log", false, 0, &rm.auditLoggers.Manager, routeID),
		slot("versioning", false, 0, &rm.versioners.Manager, routeID),
		slot("deprecation", false, 0, &rm.deprecationHandlers.Manager, routeID),
		slot("timeout", false, 0, &rm.timeoutConfigs.Manager, routeID),
		{"rate_limit", func() middleware.Middleware {
			if inner := rm.rateLimiters.GetMiddleware(routeID); inner != nil {
				return skipFlagMW(variables.SkipRateLimit, inner)
			}
			return nil
		}},
		slot("spike_arrest", false, 0, &rm.spikeArresters.Manager, routeID),
		slot("quota", false, 0, &rm.quotaEnforcers.Manager, routeID),
		slot("throttle", false, variables.SkipThrottle, &rm.throttlers.Manager, routeID),
		slot("request_queue", false, 0, &rm.requestQueues.Manager, routeID),
		{"auth", func() middleware.Middleware {
			if route.Auth.Required {
				return authMW(g, route.Auth)
			}
			return nil
		}},
		{"token_revocation", func() middleware.Middleware {
			if rm.tokenChecker != nil && route.Auth.Required {
				return rm.tokenChecker.Middleware()
			}
			return nil
		}},
		slot("token_exchange", false, 0, &rm.tokenExchangers.Manager, routeID),
		slot("claims_propagation", false, 0, &rm.claimsPropagators.Manager, routeID),
		slot("ext_auth", false, 0, &rm.extAuths.Manager, routeID),
		slot("opa", false, 0, &rm.opaEnforcers.Manager, routeID),
		slot("nonce", false, 0, &rm.nonceCheckers.Manager, routeID),
		slot("csrf", false, 0, &rm.csrfProtectors.Manager, routeID),
		slot("inbound_signing", false, 0, &rm.inboundVerifiers.Manager, routeID),
		slot("idempotency", false, 0, &rm.idempotencyHandlers.Manager, routeID),
		slot("dedup", false, 0, &rm.dedupHandlers.Manager, routeID),
		{"priority", func() middleware.Middleware {
			if rm.priorityAdmitter == nil {
				return nil
			}
			if pcfg, ok := rm.priorityConfigs.GetConfig(routeID); ok {
				return priorityMW(rm.priorityAdmitter, pcfg)
			}
			return nil
		}},
		slot("baggage", false, 0, &rm.baggagePropagators.Manager, routeID),
		{"tenant", func() middleware.Middleware {
			if rm.tenantManager == nil {
				return nil
			}
			return rm.tenantManager.Middleware(cfg.Tenant.Allowed, cfg.Tenant.Required)
		}},
		{"consumer_group", func() middleware.Middleware {
			if gm := rm.consumerGroups.GetManager(); gm != nil {
				return gm.Middleware()
			}
			return nil
		}},
		slot("cost_track", false, 0, &rm.costTrackers.Manager, routeID),
		{"request_rules", func() middleware.Middleware {
			hasReq := (rm.globalRules != nil && rm.globalRules.HasRequestRules()) ||
				(routeEngine != nil && routeEngine.HasRequestRules())
			if hasReq {
				return requestRulesMW(rm.globalRules, routeEngine)
			}
			return nil
		}},
		slot("waf", false, variables.SkipWAF, &rm.wafHandlers.Manager, routeID),
		slot("fault_injection", false, 0, &rm.faultInjectors.Manager, routeID),
		methodSlot("traffic_replay", &rm.trafficReplay.Manager, routeID, (*trafficreplay.Recorder).RecordingMiddleware),
		slot("mock", false, 0, &rm.mockHandlers.Manager, routeID),
		methodSlot("lua_request", &rm.luaScripters.Manager, routeID, (*luascript.LuaScript).RequestMiddleware),
		methodSlot("wasm_request", &rm.wasmPlugins.Manager, routeID, (*wasmPlugin.WasmPluginChain).RequestMiddleware),
		{"body_limit", func() middleware.Middleware {
			if !skipBody && route.MaxBodySize > 0 {
				return bodyLimitMW(route.MaxBodySize)
			}
			return nil
		}},
		slot("connect", false, 0, &rm.connectHandlers.Manager, routeID),
		enabledSlot("request_decompress", skipBody, 0, &rm.decompressors.Manager, routeID),
		{"bandwidth", func() middleware.Middleware {
			if skipBody {
				return nil
			}
			if bw := rm.bandwidthLimiters.Lookup(routeID); bw != nil {
				inner := bw.Middleware()
				return func(next http.Handler) http.Handler {
					h := inner(next)
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						varCtx := variables.GetFromRequest(r)
						if varCtx.Overrides != nil && varCtx.Overrides.BandwidthOverride > 0 {
							// Create ephemeral per-request bandwidth limiter with override rate
							ephemeral := trafficshape.NewBandwidthLimiter(
								varCtx.Overrides.BandwidthOverride,
								varCtx.Overrides.BandwidthOverride,
								0, 0,
							)
							ephemeral.WrapRequest(r)
							wrappedW := ephemeral.WrapResponse(w)
							next.ServeHTTP(wrappedW, r)
							return
						}
						h.ServeHTTP(w, r)
					})
				}
			}
			return nil
		}},
		slot("field_encrypt", skipBody, 0, &rm.fieldEncryptors.Manager, routeID),
		enabledSlot("validation", skipBody, variables.SkipValidation, &rm.validators.Manager, routeID),
		{"openapi_request", func() middleware.Middleware {
			if skipBody {
				return nil
			}
			if ov := rm.openapiValidators.Lookup(routeID); ov != nil {
				return openapiRequestMW(ov)
			}
			return nil
		}},
		slot("graphql", skipBody, 0, &rm.graphqlParsers.Manager, routeID),
		slot("graphql_subscription", false, 0, &rm.graphqlSubs.Manager, routeID),
		methodSlot("ai_prompt_guard", &rm.aiHandlers.Manager, routeID, (*ai.AIHandler).PromptGuardMiddleware),
		methodSlot("ai_prompt_decorate", &rm.aiHandlers.Manager, routeID, (*ai.AIHandler).PromptDecorateMiddleware),
		methodSlot("ai_rate_limit", &rm.aiHandlers.Manager, routeID, (*ai.AIHandler).AIRateLimitMiddleware),
		{"websocket", func() middleware.Middleware {
			if route.WebSocket.Enabled {
				return websocketMW(g.wsProxy, func() loadbalancer.Balancer { return rp.GetBalancer() })
			}
			return nil
		}},
		slot("sse", false, 0, &rm.sseHandlers.Manager, routeID),
		{"cache", func() middleware.Middleware {
			if skipBody {
				return nil
			}
			if ch := rm.caches.Lookup(routeID); ch != nil {
				return cacheMW(ch, g.metricsCollector, routeID)
			}
			return nil
		}},
		slot("coalesce", skipBody, 0, &rm.coalescers.Manager, routeID),
		{"circuit_breaker", func() middleware.Middleware {
			if cb := rm.circuitBreakers.Lookup(routeID); cb != nil {
				return circuitBreakerMW(cb, isGRPC)
			}
			return nil
		}},
		slot("outlier_detection", false, 0, &rm.outlierDetectors.Manager, routeID),
		{"adaptive_concurrency", func() middleware.Middleware {
			if al := rm.adaptiveLimiters.Lookup(routeID); al != nil {
				return adaptiveConcurrencyMW(al)
			}
			return nil
		}},
		slot("backpressure", false, 0, &rm.backpressureHandlers.Manager, routeID),
		slot("proxy_rate_limit", false, 0, &rm.proxyRateLimiters.Manager, routeID),
		slot("streaming", false, 0, &rm.streamHandlers.Manager, routeID),
		enabledSlot("compression", skipBody, variables.SkipCompression, &rm.compressors.Manager, routeID),
		enabledSlot("response_limit", skipBody, 0, &rm.responseLimiters.Manager, routeID),
		slot("etag", skipBody, 0, &rm.etagHandlers.Manager, routeID),
		{"response_rules", func() middleware.Middleware {
			hasResp := (rm.globalRules != nil && rm.globalRules.HasResponseRules()) ||
				(routeEngine != nil && routeEngine.HasResponseRules())
			if hasResp {
				return responseRulesMW(rm.globalRules, routeEngine)
			}
			return nil
		}},
		enabledSlot("mirror", false, variables.SkipMirror, &rm.mirrors.Manager, routeID),
		{"traffic_group", func() middleware.Middleware {
			if rp == nil {
				return nil
			}
			if wb, ok := rp.GetBalancer().(*loadbalancer.WeightedBalancer); ok && wb.HasStickyPolicy() {
				return trafficGroupMW(wb.GetStickyPolicy())
			}
			return nil
		}},
		{"session_affinity", func() middleware.Middleware {
			if rp == nil {
				return nil
			}
			if sa, ok := rp.GetBalancer().(*loadbalancer.SessionAffinityBalancer); ok {
				return sessionAffinityMW(sa)
			}
			return nil
		}},
		{"request_transform", func() middleware.Middleware {
			return requestTransformMW(route, rm.grpcHandlers.Lookup(routeID), reqBodyTransform)
		}},
		slot("body_gen", false, 0, &rm.bodyGenerators.Manager, routeID),
		slot("modifiers", false, 0, &rm.modifierChains.Manager, routeID),
		slot("param_forward", false, 0, &rm.paramForwarders.Manager, routeID),
		slot("backend_auth", false, 0, &rm.backendAuths.Manager, routeID),
		slot("backend_signing", false, 0, &rm.backendSigners.Manager, routeID),
		{"response_transform", func() middleware.Middleware {
			if !skipBody && respBodyTransform != nil {
				return transform.ResponseBodyTransformMiddleware(respBodyTransform)
			}
			return nil
		}},
		methodSlot("wasm_response", &rm.wasmPlugins.Manager, routeID, (*wasmPlugin.WasmPluginChain).ResponseMiddleware),
		methodSlot("lua_response", &rm.luaScripters.Manager, routeID, (*luascript.LuaScript).ResponseMiddleware),
		slot("jmespath", skipBody, 0, &rm.jmespathHandlers.Manager, routeID),
		slot("status_map", false, 0, &rm.statusMappers.Manager, routeID),
		slot("content_replacer", skipBody, 0, &rm.contentReplacers.Manager, routeID),
		slot("pii_redact", skipBody, 0, &rm.piiRedactors.Manager, routeID),
		slot("field_replacer", skipBody, 0, &rm.fieldReplacers.Manager, routeID),
		slot("resp_body_gen", skipBody, 0, &rm.respBodyGenerators.Manager, routeID),
		slot("error_handling", false, 0, &rm.errorHandlers.Manager, routeID),
		slot("content_neg", skipBody, 0, &rm.contentNegotiators.Manager, routeID),
		slot("response_signing", skipBody, 0, &rm.responseSigners.Manager, routeID),
	}

	// Insert custom middleware slots at anchor positions
	for _, cs := range g.customSlots {
		idx, err := resolveCustomSlotAnchor(slots, cs.After, cs.Before, cs.Name)
		if err != nil {
			logging.Error("failed to resolve custom middleware anchor",
				zap.String("middleware", cs.Name),
				zap.String("route", routeID),
				zap.Error(err))
			continue
		}
		capturedSlot := cs
		capturedCfg := cfg
		newSlot := namedSlot{
			name: cs.Name,
			build: func() middleware.Middleware {
				return capturedSlot.Build(routeID, capturedCfg)
			},
		}
		slots = slices.Insert(slots, idx, newSlot)
	}

	chain := middleware.NewBuilderWithCap(len(slots))
	for _, s := range slots {
		if mw := s.build(); mw != nil {
			chain = chain.Use(mw)
		}
	}

	// Innermost handler: aggregate, sequential, echo, static, translator, or proxy
	var innermost http.Handler
	if aggH := rm.aggregateHandlers.Lookup(routeID); aggH != nil {
		innermost = aggH
	} else if seqH := rm.sequentialHandlers.Lookup(routeID); seqH != nil {
		innermost = seqH
	} else if cfg.Echo {
		innermost = proxy.NewEchoHandler(routeID)
	} else if sh := rm.staticFiles.Lookup(routeID); sh != nil {
		innermost = sh
	} else if fcgiH := rm.fastcgiHandlers.Lookup(routeID); fcgiH != nil {
		innermost = fcgiH
	} else if fedH := rm.federationHandlers.Lookup(routeID); fedH != nil {
		innermost = fedH
	} else if aiH := rm.aiHandlers.Lookup(routeID); aiH != nil {
		innermost = aiH
	} else if translatorHandler := rm.translators.GetHandler(routeID); translatorHandler != nil {
		innermost = translatorHandler
	} else if lambdaH := rm.lambdaHandlers.Lookup(routeID); lambdaH != nil {
		innermost = lambdaH
	} else if amqpH := rm.amqpHandlers.Lookup(routeID); amqpH != nil {
		innermost = amqpH
	} else if pubsubH := rm.pubsubHandlers.Lookup(routeID); pubsubH != nil {
		innermost = pubsubH
	} else {
		innermost = rp
	}

	// gRPC reflection proxy — intercepts reflection requests before reaching the proxy
	if refProxy := rm.grpcReflection.Lookup(routeID); refProxy != nil {
		innermost = refProxy.Middleware()(innermost)
	}

	// 17.55. backendEncodingMW — wraps innermost handler directly
	if be := rm.backendEncoders.Lookup(routeID); be != nil {
		innermost = be.Middleware()(innermost)
	}

	// 17.56. isCollectionMW — wraps array responses as {"collection_key": [...]}
	if cfg.BackendResponse.IsCollection {
		innermost = isCollectionMW(cfg.BackendResponse.CollectionKey)(innermost)
	}

	// 17.5 responseValidationMW — wraps innermost (closest to proxy)
	if !skipBody {
		respValidator := rm.validators.Lookup(routeID)
		openapiV := rm.openapiValidators.Lookup(routeID)
		hasRespValidation := (respValidator != nil && respValidator.HasResponseSchema()) ||
			(openapiV != nil && openapiV.ValidatesResponse())
		if hasRespValidation {
			innermost = responseValidationMW(respValidator, openapiV)(innermost)
		}
	}

	return chain.Handler(innermost)
}

// resolveCustomSlotAnchor finds the insertion index for a custom middleware
// slot based on its After/Before anchor names.
func resolveCustomSlotAnchor(slots []namedSlot, after, before, name string) (int, error) {
	findIndex := func(anchor string) (int, error) {
		for i, s := range slots {
			if s.name == anchor {
				return i, nil
			}
		}
		return -1, fmt.Errorf("middleware %q: anchor %q not found in chain", name, anchor)
	}

	if after != "" && before != "" {
		afterIdx, err := findIndex(after)
		if err != nil {
			return 0, err
		}
		beforeIdx, err := findIndex(before)
		if err != nil {
			return 0, err
		}
		if afterIdx >= beforeIdx {
			return 0, fmt.Errorf("middleware %q: anchor %q (pos %d) must come before %q (pos %d)",
				name, after, afterIdx, before, beforeIdx)
		}
		return afterIdx + 1, nil
	}

	if after != "" {
		idx, err := findIndex(after)
		if err != nil {
			return 0, err
		}
		return idx + 1, nil
	}

	if before != "" {
		idx, err := findIndex(before)
		if err != nil {
			return 0, err
		}
		return idx, nil
	}

	return len(slots), nil
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
func (g *Runway) watchService(routeID, serviceName string, tags []string) {
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
					be := &loadbalancer.Backend{
						URL:     svc.URL(),
						Weight:  1,
						Healthy: svc.Health == registry.HealthPassing,
					}
					be.InitParsedURL()
					backends = append(backends, be)
				}

				// Update route proxy
				rp, ok := (*g.routeProxies.Load())[routeID]
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
func (g *Runway) updateBackendHealth(url string, status health.Status) {
	healthy := status == health.StatusHealthy

	for _, rp := range *g.routeProxies.Load() {
		if healthy {
			rp.GetBalancer().MarkHealthy(url)
		} else {
			rp.GetBalancer().MarkUnhealthy(url)
		}
	}
}

// Handler returns the main HTTP handler
func (g *Runway) Handler() http.Handler {
	slots := []namedSlot{
		{"recovery", func() middleware.Middleware { return middleware.Recovery() }},
		{"real_ip", func() middleware.Middleware {
			if g.realIPExtractor != nil {
				return g.realIPExtractor.Middleware
			}
			return nil
		}},
		{"https_redirect", func() middleware.Middleware {
			if g.httpsRedirect != nil {
				return g.httpsRedirect.Middleware
			}
			return nil
		}},
		{"allowed_hosts", func() middleware.Middleware {
			if g.allowedHosts != nil {
				return g.allowedHosts.Middleware
			}
			return nil
		}},
		{"request_id", func() middleware.Middleware { return middleware.RequestID() }},
		{"load_shed", func() middleware.Middleware {
			if g.loadShedder != nil {
				return g.loadShedder.Middleware()
			}
			return nil
		}},
		{"service_rate_limit", func() middleware.Middleware {
			if g.serviceLimiter != nil {
				return g.serviceLimiter.Middleware()
			}
			return nil
		}},
		{"alt_svc", func() middleware.Middleware {
			if g.http3AltSvcPort != "" {
				return altsvc.Middleware(g.http3AltSvcPort)
			}
			return nil
		}},
		{"mtls", func() middleware.Middleware { return mtls.Middleware() }},
		{"tracing", func() middleware.Middleware {
			if g.tracer != nil {
				return g.tracer.Middleware()
			}
			return nil
		}},
		{"logging", func() middleware.Middleware {
			return middleware.LoggingWithConfig(middleware.LoggingConfig{
				Format: g.config.Logging.Format,
				JSON:   true,
			})
		}},
	}

	// Insert custom global middleware slots at anchor positions
	for _, cs := range g.customGlobalSlots {
		idx, err := resolveCustomSlotAnchor(slots, cs.After, cs.Before, cs.Name)
		if err != nil {
			logging.Error("failed to resolve custom global middleware anchor",
				zap.String("middleware", cs.Name),
				zap.Error(err))
			continue
		}
		capturedSlot := cs
		capturedCfg := g.config
		slots = slices.Insert(slots, idx, namedSlot{
			name: cs.Name,
			build: func() middleware.Middleware {
				return capturedSlot.Build(capturedCfg)
			},
		})
	}

	chain := middleware.NewBuilderWithCap(len(slots))
	for _, s := range slots {
		if mw := s.build(); mw != nil {
			chain = chain.Use(mw)
		}
	}

	return chain.Handler(http.HandlerFunc(g.serveHTTP))
}

// serveHTTP handles incoming requests by dispatching to the per-route handler pipeline.
func (g *Runway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// Debug endpoint intercept (before route matching)
	if g.debugHandler != nil && g.debugHandler.Matches(r.URL.Path) {
		g.debugHandler.ServeHTTP(w, r)
		return
	}

	// SAML protocol endpoint intercept (before route matching)
	if g.samlAuth != nil && g.samlAuth.MatchesPath(r.URL.Path) {
		g.samlAuth.ServeHTTP(w, r)
		return
	}

	match := g.router.Match(r)
	if match == nil {
		errors.ErrNotFound.WriteJSON(w)
		return
	}
	defer router.ReleaseMatch(match)

	// Set path params directly on the existing varCtx (already in context from RequestID middleware).
	varCtx := variables.GetFromRequest(r)
	varCtx.PathParams = match.PathParams

	handler, ok := (*g.routeHandlers.Load())[match.Route.ID]
	if !ok {
		errors.ErrInternalServer.WithDetails("Route handler not found").WriteJSON(w)
		return
	}

	handler.ServeHTTP(w, r)
}

// authenticate handles authentication for a request
func (g *Runway) authenticate(w http.ResponseWriter, r *http.Request, methods []string) bool {
	// If no specific methods, try all available (basic/ldap excluded from default — they trigger browser dialogs)
	if len(methods) == 0 {
		methods = []string{"jwt", "api_key", "oauth"}
	}

	var identity *variables.Identity
	var err error
	hasBasicMethod := false
	hasSAMLMethod := false

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
		case "basic":
			hasBasicMethod = true
			if g.basicAuth != nil && g.basicAuth.IsEnabled() {
				identity, err = g.basicAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		case "ldap":
			hasBasicMethod = true
			if g.ldapAuth != nil && g.ldapAuth.IsEnabled() {
				identity, err = g.ldapAuth.Authenticate(r)
				if err == nil {
					break
				}
			}
		case "saml":
			hasSAMLMethod = true
			if g.samlAuth != nil && g.samlAuth.IsEnabled() {
				identity, err = g.samlAuth.Authenticate(r)
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
		// SAML-only routes: return JSON with login_url instead of WWW-Authenticate
		if hasSAMLMethod && !hasBasicMethod {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			loginURL := "/saml/login"
			if g.samlAuth != nil {
				loginURL = g.samlAuth.PathPrefix() + "login"
			}
			fmt.Fprintf(w, `{"error":"unauthorized","login_url":%q}`, loginURL)
			return false
		}
		// Build WWW-Authenticate header dynamically
		wwwAuth := `Bearer realm="api", API-Key`
		if hasBasicMethod {
			realm := "Restricted"
			if g.basicAuth != nil {
				realm = g.basicAuth.Realm()
			} else if g.ldapAuth != nil {
				realm = g.ldapAuth.Realm()
			}
			wwwAuth = fmt.Sprintf(`Basic realm="%s"`, realm)
		}
		w.Header().Set("WWW-Authenticate", wwwAuth)
		errors.ErrUnauthorized.WriteJSON(w)
		return false
	}

	// Add identity to context
	varCtx := variables.GetFromRequest(r)
	varCtx.Identity = identity

	return true
}

// Close closes the gateway and releases resources
func (g *Runway) Close() error {
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

	// Close LDAP connection pool
	if g.ldapAuth != nil {
		g.ldapAuth.Close()
	}

	// Close SAML metadata refresh
	if g.samlAuth != nil {
		g.samlAuth.Close()
	}

	// Close tracer
	if g.tracer != nil {
		g.tracer.Close()
	}

	// Close tenant manager
	if g.tenantManager != nil {
		g.tenantManager.Close()
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
	g.adaptiveLimiters.CloseAll()

	// Stop outlier detectors
	g.outlierDetectors.StopAll()

	// Close nonce checkers
	byroute.ForEach(&g.nonceCheckers.Manager, func(nc *nonce.NonceChecker) { nc.CloseStore() })

	// Close idempotency handlers
	byroute.ForEach(&g.idempotencyHandlers.Manager, (*idempotency.CompiledIdempotency).Close)

	// Close token revocation checker
	if g.tokenChecker != nil {
		g.tokenChecker.Close()
	}

	// Close load shedder
	if g.loadShedder != nil {
		g.loadShedder.Close()
	}

	// Close backpressure handlers
	byroute.ForEach(&g.backpressureHandlers.Manager, (*backpressure.Backpressure).Close)

	// Close audit loggers
	byroute.ForEach(&g.auditLoggers.Manager, (*auditlog.AuditLogger).Close)

	// Close ext auth clients
	byroute.ForEach(&g.extAuths.Manager, (*extauth.ExtAuth).Close)

	// Close geo provider
	if g.geoProvider != nil {
		g.geoProvider.Close()
	}

	// Stop SSE fan-out hubs
	byroute.ForEach(&g.sseHandlers.Manager, (*sse.SSEHandler).StopHub)

	// Close WASM plugin runtime and pools
	g.wasmPlugins.Close(context.Background())

	// Close protocol translators
	g.translators.Close()

	// Close registry
	if g.registry != nil {
		return g.registry.Close()
	}

	return nil
}

// GetRouter returns the router
func (g *Runway) GetRouter() *router.Router {
	return g.router
}

// GetCatalogBuilder returns the API catalog builder, or nil if catalog is disabled.
func (g *Runway) GetCatalogBuilder() *catalog.Builder {
	return g.catalogBuilder
}

// GetRegistry returns the registry
func (g *Runway) GetRegistry() registry.Registry {
	return g.registry
}

// GetHealthChecker returns the health checker
func (g *Runway) GetHealthChecker() *health.Checker {
	return g.healthChecker
}

// GetCircuitBreakers returns the circuit breaker manager
func (g *Runway) GetCircuitBreakers() *circuitbreaker.BreakerByRoute {
	return g.circuitBreakers
}

// GetCaches returns the cache manager
func (g *Runway) GetCaches() *cache.CacheByRoute {
	return g.caches
}

// GetRetryMetrics returns the retry metrics per route
func (g *Runway) GetRetryMetrics() map[string]*retry.RouteRetryMetrics {
	result := make(map[string]*retry.RouteRetryMetrics)
	for routeID, rp := range *g.routeProxies.Load() {
		if m := rp.GetRetryMetrics(); m != nil {
			result[routeID] = m
		}
	}
	return result
}

// GetMetricsCollector returns the metrics collector
func (g *Runway) GetMetricsCollector() *metrics.Collector {
	return g.metricsCollector
}

// GetGlobalRules returns the global rules engine (may be nil).
func (g *Runway) GetGlobalRules() *rules.RuleEngine {
	return g.globalRules
}

// GetRouteRules returns the per-route rules manager.
func (g *Runway) GetRouteRules() *rules.RulesByRoute {
	return g.routeRules
}

// GetTranslators returns the protocol translator manager.
func (g *Runway) GetTranslators() *protocol.TranslatorByRoute {
	return g.translators
}

// GetThrottlers returns the throttle manager.
func (g *Runway) GetThrottlers() *trafficshape.ThrottleByRoute {
	return g.throttlers
}

// GetBandwidthLimiters returns the bandwidth limiter manager.
func (g *Runway) GetBandwidthLimiters() *trafficshape.BandwidthByRoute {
	return g.bandwidthLimiters
}

// GetPriorityAdmitter returns the priority admitter (may be nil).
func (g *Runway) GetPriorityAdmitter() *trafficshape.PriorityAdmitter {
	return g.priorityAdmitter
}

// GetFaultInjectors returns the fault injection manager.
func (g *Runway) GetFaultInjectors() *trafficshape.FaultInjectionByRoute {
	return g.faultInjectors
}

// GetRateLimiters returns the rate limiter manager.
func (g *Runway) GetRateLimiters() *ratelimit.RateLimitByRoute {
	return g.rateLimiters
}

// GetMirrors returns the mirror manager.
func (g *Runway) GetMirrors() *mirror.MirrorByRoute {
	return g.mirrors
}

// GetGraphQLParsers returns the GraphQL parser manager.
func (g *Runway) GetGraphQLParsers() *graphql.GraphQLByRoute {
	return g.graphqlParsers
}

// GetCanaryControllers returns the canary controller manager.
func (g *Runway) GetCanaryControllers() *canary.CanaryByRoute {
	return g.canaryControllers
}

// GetBlueGreenControllers returns the blue-green controller manager.
func (g *Runway) GetBlueGreenControllers() *bluegreen.BlueGreenByRoute {
	return g.blueGreenControllers
}

// GetABTests returns the A/B test manager.
func (g *Runway) GetABTests() *abtest.ABTestByRoute {
	return g.abTests
}

// GetTrafficReplay returns the traffic replay manager.
func (g *Runway) GetTrafficReplay() *trafficreplay.ReplayByRoute {
	return g.trafficReplay
}

// GetRequestQueues returns the request queue manager.
func (g *Runway) GetRequestQueues() *requestqueue.RequestQueueByRoute {
	return g.requestQueues
}

// GetAdaptiveLimiters returns the adaptive concurrency limiter manager.
func (g *Runway) GetAdaptiveLimiters() *trafficshape.AdaptiveConcurrencyByRoute {
	return g.adaptiveLimiters
}

// GetErrorPages returns the error pages manager.
func (g *Runway) GetErrorPages() *errorpages.ErrorPagesByRoute {
	return g.errorPages
}

// GetMaintenanceHandlers returns the maintenance ByRoute manager.
func (g *Runway) GetMaintenanceHandlers() *maintenance.MaintenanceByRoute {
	return g.maintenanceHandlers
}

// GetHTTPSRedirect returns the HTTPS redirect handler (may be nil).
func (g *Runway) GetHTTPSRedirect() *httpsredirect.CompiledHTTPSRedirect {
	return g.httpsRedirect
}

// GetAllowedHosts returns the allowed hosts handler (may be nil).
func (g *Runway) GetAllowedHosts() *allowedhosts.CompiledAllowedHosts {
	return g.allowedHosts
}

// GetTokenChecker returns the token revocation checker (may be nil).
func (g *Runway) GetTokenChecker() *tokenrevoke.TokenChecker {
	return g.tokenChecker
}

// GetUpstreams returns the configured upstream map.
func (g *Runway) GetUpstreams() map[string]config.UpstreamConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.config.Upstreams
}

// GetFollowRedirectStats returns per-route redirect transport stats.
func (g *Runway) GetFollowRedirectStats() map[string]interface{} {
	result := make(map[string]interface{})
	for routeID, rp := range *g.routeProxies.Load() {
		if rt := rp.GetRedirectTransport(); rt != nil {
			result[routeID] = rt.Stats()
		}
	}
	return result
}

// GetTransportPool returns the proxy's transport pool.
func (g *Runway) GetTransportPool() *proxy.TransportPool {
	return g.proxy.GetTransportPool()
}

// buildTransportPool constructs a TransportPool from the config.
// Three-level merge: defaults → global transport → per-upstream transport.
func (g *Runway) buildTransportPool(cfg *config.Config) *proxy.TransportPool {
	// Start from defaults, apply global config
	baseCfg := proxy.MergeTransportConfigs(proxy.DefaultTransportConfig, cfg.Transport)

	// Apply DNS resolver if configured
	if len(cfg.DNSResolver.Nameservers) > 0 {
		baseCfg.Resolver = proxy.NewResolver(cfg.DNSResolver.Nameservers, cfg.DNSResolver.Timeout)
	}

	// Apply SSRF protection if configured
	if cfg.SSRFProtection.Enabled {
		baseCfg.SSRFProtection = &cfg.SSRFProtection
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
func (g *Runway) GetLoadBalancerInfo() map[string]interface{} {
	proxies := *g.routeProxies.Load()
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
			if rp, ok := proxies[routeCfg.ID]; ok {
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
func (g *Runway) GetTrafficSplitStats() map[string]interface{} {
	result := make(map[string]interface{})
	for routeID, rp := range *g.routeProxies.Load() {
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

// GetAPIKeyAuth returns the API key auth for admin API
func (g *Runway) GetAPIKeyAuth() *auth.APIKeyAuth {
	return g.apiKeyAuth
}

// GetBasicAuth returns the basic auth provider.
func (g *Runway) GetBasicAuth() *auth.BasicAuth {
	return g.basicAuth
}

// GetLDAPAuth returns the LDAP auth provider.
func (g *Runway) GetLDAPAuth() *auth.LDAPAuth {
	return g.ldapAuth
}

// GetSAMLAuth returns the SAML auth provider.
func (g *Runway) GetSAMLAuth() *auth.SAMLAuth {
	return g.samlAuth
}

// Stats returns gateway statistics
type Stats struct {
	Routes        int            `json:"routes"`
	HealthyRoutes int            `json:"healthy_routes"`
	Backends      map[string]int `json:"backends"`
}

// GetStats returns current gateway statistics
func (g *Runway) GetStats() *Stats {
	proxies := *g.routeProxies.Load()
	stats := &Stats{
		Routes:   len(proxies),
		Backends: make(map[string]int),
	}

	for routeID, rp := range proxies {
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
