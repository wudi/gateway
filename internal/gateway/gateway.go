package gateway

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/health"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/middleware/auth"
	"github.com/example/gateway/internal/middleware/ratelimit"
	"github.com/example/gateway/internal/middleware/transform"
	"github.com/example/gateway/internal/proxy"
	"github.com/example/gateway/internal/registry"
	"github.com/example/gateway/internal/registry/consul"
	"github.com/example/gateway/internal/registry/etcd"
	"github.com/example/gateway/internal/registry/memory"
	"github.com/example/gateway/internal/router"
	"github.com/example/gateway/internal/variables"
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
	rateLimiters  *ratelimit.RateLimitByRoute
	resolver      *variables.Resolver

	routeProxies map[string]*proxy.RouteProxy
	watchCancels map[string]context.CancelFunc
	mu           sync.RWMutex
}

// New creates a new gateway
func New(cfg *config.Config) (*Gateway, error) {
	g := &Gateway{
		config:        cfg,
		router:        router.New(),
		rateLimiters:  ratelimit.NewRateLimitByRoute(),
		resolver:      variables.NewResolver(),
		routeProxies:  make(map[string]*proxy.RouteProxy),
		watchCancels:  make(map[string]context.CancelFunc),
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

	// Create route proxy
	g.mu.Lock()
	g.routeProxies[routeCfg.ID] = proxy.NewRouteProxy(g.proxy, route, backends)
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
		Use(middleware.RequestID()).
		Use(middleware.LoggingWithConfig(middleware.LoggingConfig{
			Format: g.config.Logging.Format,
			JSON:   g.config.Logging.Level == "json",
		})).
		Use(g.rateLimiters.Middleware())

	return chain.Handler(http.HandlerFunc(g.serveHTTP))
}

// serveHTTP handles incoming requests
func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// Match route
	match := g.router.Match(r)
	if match == nil {
		errors.ErrNotFound.WriteJSON(w)
		return
	}

	route := match.Route

	// Get variable context
	varCtx := variables.GetFromRequest(r)
	varCtx.RouteID = route.ID
	varCtx.PathParams = match.PathParams
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	r = r.WithContext(ctx)

	// Handle authentication
	if route.Auth.Required {
		if !g.authenticate(w, r, route.Auth.Methods) {
			return
		}
	}

	// Get route proxy
	g.mu.RLock()
	rp, ok := g.routeProxies[route.ID]
	g.mu.RUnlock()

	if !ok {
		errors.ErrInternalServer.WithDetails("Route proxy not found").WriteJSON(w)
		return
	}

	// Apply request transformations
	transformer := transform.NewHeaderTransformer()
	transformer.TransformRequest(r, route.Transform.Request.Headers, varCtx)

	// Proxy the request
	rp.ServeHTTP(w, r)
}

// authenticate handles authentication for a request
func (g *Gateway) authenticate(w http.ResponseWriter, r *http.Request, methods []string) bool {
	// If no specific methods, try all available
	if len(methods) == 0 {
		methods = []string{"jwt", "api_key"}
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
