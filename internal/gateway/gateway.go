package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/example/gateway/internal/cache"
	"github.com/example/gateway/internal/circuitbreaker"
	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/health"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/metrics"
	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/middleware/auth"
	"github.com/example/gateway/internal/middleware/compression"
	"github.com/example/gateway/internal/middleware/cors"
	"github.com/example/gateway/internal/middleware/ipfilter"
	"github.com/example/gateway/internal/middleware/ratelimit"
	"github.com/example/gateway/internal/middleware/transform"
	"github.com/example/gateway/internal/middleware/validation"
	"github.com/example/gateway/internal/mirror"
	grpcproxy "github.com/example/gateway/internal/proxy/grpc"
	"github.com/example/gateway/internal/proxy"
	"github.com/example/gateway/internal/proxy/tcp"
	"github.com/example/gateway/internal/proxy/udp"
	"github.com/example/gateway/internal/registry"
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
	tcpProxy      *tcp.Proxy
	udpProxy      *udp.Proxy
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

	routeProxies map[string]*proxy.RouteProxy
	watchCancels map[string]context.CancelFunc
	mu           sync.RWMutex
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

// bodyBufferResponseWriter captures the response body for transformation before writing
type bodyBufferResponseWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	header     http.Header
}

func newBodyBufferResponseWriter(w http.ResponseWriter) *bodyBufferResponseWriter {
	return &bodyBufferResponseWriter{
		ResponseWriter: w,
		statusCode:     200,
		header:         make(http.Header),
	}
}

func (bw *bodyBufferResponseWriter) Header() http.Header {
	return bw.header
}

func (bw *bodyBufferResponseWriter) WriteHeader(code int) {
	bw.statusCode = code
}

func (bw *bodyBufferResponseWriter) Write(b []byte) (int, error) {
	return bw.body.Write(b)
}

// replayTo writes the buffered response (with transformed body) through the real writer chain.
func (bw *bodyBufferResponseWriter) replayTo(w http.ResponseWriter, body []byte) {
	// Copy captured headers to real writer
	for k, vv := range bw.header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	// Update Content-Length for transformed body
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(bw.statusCode)
	w.Write(body)
}

// applyResponseBodyTransform applies response body transformations to JSON bodies
func applyResponseBodyTransform(body []byte, cfg config.BodyTransformConfig) []byte {
	// Only transform JSON
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return body
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
		return body
	}
	return newBody
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
		routeProxies:     make(map[string]*proxy.RouteProxy),
		watchCancels:     make(map[string]context.CancelFunc),
	}

	// Initialize global IP filter
	if cfg.IPFilter.Enabled {
		var err error
		g.globalIPFilter, err = ipfilter.New(cfg.IPFilter)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize global IP filter: %w", err)
		}
	}

	// Initialize tracer
	if cfg.Tracing.Enabled {
		g.tracer = tracing.New(cfg.Tracing)
	}

	// Initialize health checker
	g.healthChecker = health.NewChecker(health.Config{
		OnChange: func(url string, status health.Status) {
			log.Printf("Backend %s health changed to %s", url, status)
			g.updateBackendHealth(url, status)
		},
	})

	// Initialize proxy
	g.proxy = proxy.New(proxy.Config{
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

	// Initialize TCP/UDP proxies if needed
	if err := g.initL4Proxies(); err != nil {
		return nil, fmt.Errorf("failed to initialize L4 proxies: %w", err)
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

// initL4Proxies initializes TCP and UDP proxies if routes are configured
func (g *Gateway) initL4Proxies() error {
	// Initialize TCP proxy if TCP routes are configured
	if len(g.config.TCPRoutes) > 0 {
		g.tcpProxy = tcp.NewProxy(tcp.Config{})

		for _, routeCfg := range g.config.TCPRoutes {
			if err := g.tcpProxy.AddRoute(routeCfg); err != nil {
				return fmt.Errorf("failed to add TCP route %s: %w", routeCfg.ID, err)
			}
		}
		log.Printf("Initialized TCP proxy with %d routes", len(g.config.TCPRoutes))
	}

	// Initialize UDP proxy if UDP routes are configured
	if len(g.config.UDPRoutes) > 0 {
		g.udpProxy = udp.NewProxy(udp.Config{})

		for _, routeCfg := range g.config.UDPRoutes {
			if err := g.udpProxy.AddRoute(routeCfg); err != nil {
				return fmt.Errorf("failed to add UDP route %s: %w", routeCfg.ID, err)
			}
		}
		log.Printf("Initialized UDP proxy with %d routes", len(g.config.UDPRoutes))
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
			log.Printf("Warning: failed to discover service %s: %v", routeCfg.Service.Name, err)
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

	// Create route proxy (with weighted balancer if traffic split is configured)
	g.mu.Lock()
	if len(routeCfg.TrafficSplit) > 0 {
		// Feature 6: Use weighted balancer for traffic splitting
		wb := loadbalancer.NewWeightedBalancer(routeCfg.TrafficSplit)
		g.routeProxies[routeCfg.ID] = proxy.NewRouteProxyWithBalancer(g.proxy, route, wb)
	} else {
		g.routeProxies[routeCfg.ID] = proxy.NewRouteProxy(g.proxy, route, backends)
	}
	g.mu.Unlock()

	// Set up rate limiting
	if routeCfg.RateLimit.Enabled || routeCfg.RateLimit.Rate > 0 {
		g.rateLimiters.AddRoute(routeCfg.ID, ratelimit.Config{
			Rate:   routeCfg.RateLimit.Rate,
			Period: routeCfg.RateLimit.Period,
			Burst:  routeCfg.RateLimit.Burst,
			PerIP:  routeCfg.RateLimit.PerIP,
		})
	}

	// Set up circuit breaker
	if routeCfg.CircuitBreaker.Enabled {
		g.circuitBreakers.AddRoute(routeCfg.ID, routeCfg.CircuitBreaker)
	}

	// Set up cache
	if routeCfg.Cache.Enabled {
		g.caches.AddRoute(routeCfg.ID, routeCfg.Cache)
	}

	// Feature 2: Set up per-route IP filter
	if routeCfg.IPFilter.Enabled {
		if err := g.ipFilters.AddRoute(routeCfg.ID, routeCfg.IPFilter); err != nil {
			return fmt.Errorf("failed to add IP filter for route %s: %w", routeCfg.ID, err)
		}
	}

	// Feature 3: Set up CORS
	if routeCfg.CORS.Enabled {
		g.corsHandlers.AddRoute(routeCfg.ID, routeCfg.CORS)
	}

	// Feature 4: Set up compression
	if routeCfg.Compression.Enabled {
		g.compressors.AddRoute(routeCfg.ID, routeCfg.Compression)
	}

	// Feature 8: Set up request validation
	if routeCfg.Validation.Enabled {
		if err := g.validators.AddRoute(routeCfg.ID, routeCfg.Validation); err != nil {
			return fmt.Errorf("failed to add validator for route %s: %w", routeCfg.ID, err)
		}
	}

	// Feature 10: Set up traffic mirroring
	if routeCfg.Mirror.Enabled {
		g.mirrors.AddRoute(routeCfg.ID, routeCfg.Mirror)
	}

	// Feature 12: Set up gRPC handler once per route
	if routeCfg.GRPC.Enabled {
		g.grpcHandlers[routeCfg.ID] = grpcproxy.New(true)
	}

	return nil
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
			log.Printf("Failed to watch service %s: %v", serviceName, err)
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
					log.Printf("Updated backends for route %s: %d services", routeID, len(backends))
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
	// Build middleware chain
	chain := middleware.NewBuilder().
		Use(middleware.Recovery()).
		Use(middleware.RequestID())

	// Feature 9: Add tracing middleware
	if g.tracer != nil {
		chain = chain.Use(g.tracer.Middleware())
	}

	chain = chain.
		Use(middleware.LoggingWithConfig(middleware.LoggingConfig{
			Format: g.config.Logging.Format,
			JSON:   g.config.Logging.Level == "json",
		})).
		Use(g.rateLimiters.Middleware())

	return chain.Handler(http.HandlerFunc(g.serveHTTP))
}

// serveHTTP handles incoming requests
func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	requestStart := time.Now()

	// Step 1: Match route
	match := g.router.Match(r)
	if match == nil {
		errors.ErrNotFound.WriteJSON(w)
		return
	}

	route := match.Route

	// Step 1.1: IP filter check (Feature 2)
	// Check global IP filter first
	if g.globalIPFilter != nil && !g.globalIPFilter.Check(r) {
		ipfilter.RejectRequest(w)
		return
	}
	// Check per-route IP filter
	if !g.ipFilters.CheckRequest(route.ID, r) {
		ipfilter.RejectRequest(w)
		return
	}

	// Step 1.5: CORS preflight check (Feature 3)
	if corsHandler := g.corsHandlers.GetHandler(route.ID); corsHandler != nil && corsHandler.IsEnabled() {
		if corsHandler.IsPreflight(r) {
			corsHandler.HandlePreflight(w, r)
			return
		}
		// Non-preflight: apply response headers
		corsHandler.ApplyHeaders(w, r)
	}

	// Step 2: Variable context setup
	varCtx := variables.GetFromRequest(r)
	varCtx.RouteID = route.ID
	varCtx.PathParams = match.PathParams
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	r = r.WithContext(ctx)

	// Step 3: Authentication (+ OAuth Feature 7)
	if route.Auth.Required {
		if !g.authenticate(w, r, route.Auth.Methods) {
			return
		}
	}

	// Step 4: Get route proxy
	g.mu.RLock()
	rp, ok := g.routeProxies[route.ID]
	g.mu.RUnlock()

	if !ok {
		errors.ErrInternalServer.WithDetails("Route proxy not found").WriteJSON(w)
		return
	}

	// Step 4.5: Body size limit check (Feature 1)
	if route.MaxBodySize > 0 {
		// Check Content-Length for early rejection
		if r.ContentLength > route.MaxBodySize {
			errors.ErrRequestEntityTooLarge.WithDetails(
				fmt.Sprintf("Request body exceeds maximum size of %d bytes", route.MaxBodySize),
			).WriteJSON(w)
			return
		}
		// Wrap body with MaxBytesReader for chunked transfers
		r.Body = http.MaxBytesReader(w, r.Body, route.MaxBodySize)
	}

	// Step 4.6: Request validation (Feature 8)
	if v := g.validators.GetValidator(route.ID); v != nil && v.IsEnabled() {
		if err := v.Validate(r); err != nil {
			validation.RejectValidation(w, err)
			return
		}
	}

	// Step 5: WebSocket upgrade — bypass cache/circuit breaker
	if route.WebSocket.Enabled && websocket.IsUpgradeRequest(r) {
		backend := rp.GetBalancer().Next()
		if backend == nil {
			errors.ErrServiceUnavailable.WithDetails("No healthy backends available").WriteJSON(w)
			return
		}
		g.wsProxy.ServeHTTP(w, r, backend.URL)
		return
	}

	// Step 6: Cache HIT — return early
	if cacheHandler := g.caches.GetHandler(route.ID); cacheHandler != nil {
		if cacheHandler.ShouldCache(r) {
			if entry, ok := cacheHandler.Get(r); ok {
				// Feature 5: Record cache hit metric
				g.metricsCollector.RecordCacheHit(route.ID)
				cache.WriteCachedResponse(w, entry)
				g.recordMetrics(route.ID, r.Method, 200, time.Since(requestStart))
				return
			}
			// Feature 5: Record cache miss
			g.metricsCollector.RecordCacheMiss(route.ID)
		}

		// Invalidate cache on mutating requests
		if cache.IsMutatingMethod(r.Method) {
			cacheHandler.InvalidateByPath(r.URL.Path)
		}
	}

	// Step 7: Circuit breaker check
	cb := g.circuitBreakers.GetBreaker(route.ID)
	var cbDone func(error)
	if cb != nil {
		var err error
		cbDone, err = cb.Allow()
		if err != nil {
			errors.ErrServiceUnavailable.WithDetails("Circuit breaker is open").WriteJSON(w)
			g.recordMetrics(route.ID, r.Method, 503, time.Since(requestStart))
			return
		}
	}

	// Step 8: Conditional ResponseWriter wrapping
	cacheHandler := g.caches.GetHandler(route.ID)
	shouldCache := cacheHandler != nil && cacheHandler.ShouldCache(r)

	var recorder *statusRecorder
	var cachingWriter *cache.CachingResponseWriter
	var compressingWriter *compression.CompressingResponseWriter
	var responseWriter http.ResponseWriter = w

	// Only wrap when needed
	if cbDone != nil {
		recorder = &statusRecorder{ResponseWriter: w, statusCode: 200}
		responseWriter = recorder
	}

	if shouldCache {
		if recorder == nil {
			recorder = &statusRecorder{ResponseWriter: w, statusCode: 200}
			responseWriter = recorder
		}
		cachingWriter = cache.NewCachingResponseWriter(recorder)
		responseWriter = cachingWriter
	}

	// Feature 4: Response compression wrapping (after cache so cached data is uncompressed)
	compressor := g.compressors.GetCompressor(route.ID)
	if compressor != nil && compressor.ShouldCompress(r) {
		compressingWriter = compression.NewCompressingResponseWriter(responseWriter, compressor)
		responseWriter = compressingWriter
		// Strip Accept-Encoding from forwarded request so backend returns uncompressed
		r.Header.Del("Accept-Encoding")
	}

	// Feature 10: Buffer request body if mirroring is enabled
	var mirrorBody []byte
	mirrorHandler := g.mirrors.GetMirror(route.ID)
	if mirrorHandler != nil && mirrorHandler.IsEnabled() && mirrorHandler.ShouldMirror() {
		var err error
		mirrorBody, err = mirror.BufferRequestBody(r)
		if err != nil {
			// Body read error, but continue with primary request
			mirrorBody = nil
		}
	}

	// Step 9: Apply request transformations
	transformer := transform.NewHeaderTransformer()
	transformer.TransformRequest(r, route.Transform.Request.Headers, varCtx)

	// Feature 13: Apply request body transformations
	if len(route.Transform.Request.Body.AddFields) > 0 || len(route.Transform.Request.Body.RemoveFields) > 0 || len(route.Transform.Request.Body.RenameFields) > 0 {
		applyBodyTransform(r, route.Transform.Request.Body)
	}

	// Feature 12: Prepare gRPC request if needed
	var isGRPC bool
	if grpcHandler := g.grpcHandlers[route.ID]; grpcHandler != nil && grpcproxy.IsGRPCRequest(r) {
		grpcHandler.PrepareRequest(r)
		isGRPC = true
	}

	// Step 9.5: Check if response body transforms are configured
	respBodyCfg := route.Transform.Response.Body
	hasRespBodyTransform := len(respBodyCfg.AddFields) > 0 || len(respBodyCfg.RemoveFields) > 0 || len(respBodyCfg.RenameFields) > 0
	var bodyBufWriter *bodyBufferResponseWriter
	proxyTarget := responseWriter
	if hasRespBodyTransform {
		bodyBufWriter = newBodyBufferResponseWriter(responseWriter)
		proxyTarget = bodyBufWriter
	}

	// Step 10: Proxy the request (retry policy handled inside proxy)
	if cachingWriter != nil {
		cachingWriter.Header().Set("X-Cache", "MISS")
	}
	rp.ServeHTTP(proxyTarget, r)

	// Step 10.1: Apply response body transform and replay
	if bodyBufWriter != nil {
		transformed := applyResponseBodyTransform(bodyBufWriter.body.Bytes(), respBodyCfg)
		bodyBufWriter.replayTo(responseWriter, transformed)
	}

	// Step 10.5: Mirror request async (Feature 10)
	if mirrorHandler != nil && mirrorBody != nil {
		mirrorHandler.SendAsync(r, mirrorBody)
	}

	// Feature 4: Close compression writer
	if compressingWriter != nil {
		compressingWriter.Close()
	}

	// Step 11: Record circuit breaker outcome
	finalStatus := 200
	if recorder != nil {
		finalStatus = recorder.statusCode
	}

	if cbDone != nil && recorder != nil {
		cbStatus := recorder.statusCode
		// For gRPC, check the Grpc-Status header since HTTP 200 may still be a failure
		if isGRPC && recorder.statusCode == 200 {
			grpcStatus := responseWriter.Header().Get("Grpc-Status")
			if grpcStatus != "" && grpcStatus != "0" {
				cbStatus = 500 // Treat non-OK gRPC status as failure
			}
		}
		if cbStatus >= 500 {
			cbDone(fmt.Errorf("server error: %d", cbStatus))
		} else {
			cbDone(nil)
		}
	}

	// Step 12: Store cacheable response
	if cachingWriter != nil && cacheHandler != nil {
		if cacheHandler.ShouldStore(cachingWriter.StatusCode, cachingWriter.Header(), int64(cachingWriter.Body.Len())) {
			entry := &cache.Entry{
				StatusCode: cachingWriter.StatusCode,
				Headers:    cachingWriter.Header().Clone(),
				Body:       cachingWriter.Body.Bytes(),
			}
			cacheHandler.Store(r, entry)
		}
		finalStatus = cachingWriter.StatusCode
	}

	// Step 13: Record Prometheus metrics (Feature 5)
	g.recordMetrics(route.ID, r.Method, finalStatus, time.Since(requestStart))
}

// recordMetrics records request metrics
func (g *Gateway) recordMetrics(routeID, method string, status int, duration time.Duration) {
	g.metricsCollector.RecordRequest(routeID, method, status, duration)
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

	// Close TCP proxy
	if g.tcpProxy != nil {
		g.tcpProxy.Close()
	}

	// Close UDP proxy
	if g.udpProxy != nil {
		g.udpProxy.Close()
	}

	// Close tracer
	if g.tracer != nil {
		g.tracer.Close()
	}

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

// GetTCPProxy returns the TCP proxy
func (g *Gateway) GetTCPProxy() *tcp.Proxy {
	return g.tcpProxy
}

// GetUDPProxy returns the UDP proxy
func (g *Gateway) GetUDPProxy() *udp.Proxy {
	return g.udpProxy
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
