package runway

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/logging"
	"github.com/wudi/runway/internal/middleware/allowedhosts"
	"github.com/wudi/runway/internal/middleware/debug"
	"github.com/wudi/runway/internal/middleware/httpsredirect"
	"github.com/wudi/runway/internal/middleware/loadshed"
	openapivalidation "github.com/wudi/runway/internal/middleware/openapi"
	"github.com/wudi/runway/internal/middleware/serviceratelimit"
	"github.com/wudi/runway/internal/proxy"
	"github.com/wudi/runway/internal/registry"
	"github.com/wudi/runway/internal/router"
	"github.com/wudi/runway/internal/webhook"
	"github.com/wudi/runway/internal/websocket"
	"github.com/wudi/runway/variables"
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

	// All per-route and per-reload managers (same struct as Runway)
	routeManagers
}

// buildState builds all route-scoped state from a config.
// Shared infrastructure (proxy, healthChecker, registry, metricsCollector, redisClient, tracer) is
// passed via the Runway and reused without replacement.
func (g *Runway) buildState(cfg *config.Config) (*gatewayState, error) {
	s := &gatewayState{
		config:        cfg,
		router:        router.New(),
		routeProxies:  make(map[string]*proxy.RouteProxy),
		routeHandlers: make(map[string]http.Handler),
		watchCancels:  make(map[string]context.CancelFunc),
		routeManagers: newRouteManagers(cfg, g.redisClient),
	}

	// Initialize global singletons (shared between New and Reload)
	if err := s.routeManagers.initGlobals(cfg, g.redisClient); err != nil {
		return nil, err
	}

	// Register per-route features (shared between New and Reload)
	s.features = buildFeatures(&s.routeManagers, cfg, g.redisClient)

	// Wire webhook callbacks on new state's managers
	s.routeManagers.wireWebhookCallbacks(g.webhookDispatcher)

	// Initialize authentication (shared between New and Reload)
	if err := s.routeManagers.initAuth(cfg); err != nil {
		return nil, err
	}

	// Rebuild transport pool from new config and swap onto shared proxy
	newPool := g.buildTransportPool(cfg)
	oldPool := g.proxy.GetTransportPool()
	g.proxy.SetTransportPool(newPool)
	if oldPool != nil {
		oldPool.CloseIdleConnections()
	}

	// Initialize each route using a temporary Runway view so addRouteForState works
	for _, routeCfg := range cfg.Routes {
		if err := g.addRouteForState(s, routeCfg); err != nil {
			// Clean up translators on failure
			s.translators.Close()
			return nil, fmt.Errorf("failed to add route %s: %w", routeCfg.ID, err)
		}
	}

	return s, nil
}

// addRouteForState adds a single route into the given gatewayState, using the Runway's
// shared infrastructure (proxy, healthChecker, registry, redisClient).
func (g *Runway) addRouteForState(s *gatewayState, routeCfg config.RouteConfig) error {
	return g.setupRoute(&routeSetup{
		cfg:             s.config,
		rtr:             s.router,
		rm:              &s.routeManagers,
		features:        s.features,
		registerBackend: g.healthChecker.UpdateBackend,
		watchService: func(routeID, serviceName string, tags []string) {
			watchCtx, cancel := context.WithCancel(context.Background())
			s.watchCancels[routeID] = cancel
			go g.watchServiceForState(s, watchCtx, routeID, serviceName, tags)
		},
		storeProxy: func(id string, rp *proxy.RouteProxy) { s.routeProxies[id] = rp },
		buildHandler: func(routeID string, cfg config.RouteConfig, route *router.Route, rp *proxy.RouteProxy) http.Handler {
			return g.buildRouteHandler(&s.routeManagers, routeID, cfg, route, rp)
		},
		storeHandler: func(id string, h http.Handler) { s.routeHandlers[id] = h },
	}, routeCfg)
}

// watchServiceForState is like watchService but writes to a gatewayState's routeProxies.
func (g *Runway) watchServiceForState(s *gatewayState, ctx context.Context, routeID, serviceName string, tags []string) {
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
				be := &loadbalancer.Backend{
					URL:     svc.URL(),
					Weight:  1,
					Healthy: svc.Health == registry.HealthPassing,
				}
				be.InitParsedURL()
				backends = append(backends, be)
			}

			// The state's routeProxies are accessed by the Runway under g.mu,
			// but since this watcher was started for the new state it's safe to
			// read from it directly — the map doesn't change after buildState returns.
			if rp, ok := s.routeProxies[routeID]; ok {
				rp.UpdateBackends(backends)
			}
		}
	}
}

// Reload atomically replaces all route-scoped state with a new config.
// Shared infrastructure (proxy, healthChecker, registry, metricsCollector, redisClient, tracer) is preserved.
// In-flight requests complete with the old handler (handler refs are grabbed under RLock).
func (g *Runway) Reload(newCfg *config.Config) ReloadResult {
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

	// Schema evolution check (before state swap)
	if g.schemaChecker != nil && newCfg.OpenAPI.SchemaEvolution.Enabled {
		for _, specCfg := range newCfg.OpenAPI.Specs {
			if specCfg.File != "" {
				doc, loadErr := openapivalidation.LoadSpec(specCfg.File)
				if loadErr == nil {
					if _, checkErr := g.schemaChecker.CheckAndStore(specCfg.ID, doc); checkErr != nil {
						result.Error = checkErr.Error()
						return result
					}
				}
			}
		}
		for _, rc := range newCfg.Routes {
			if rc.OpenAPI.SpecFile != "" {
				doc, loadErr := openapivalidation.LoadSpec(rc.OpenAPI.SpecFile)
				if loadErr == nil {
					specID := rc.OpenAPI.SpecFile
					if rc.OpenAPI.SpecID != "" {
						specID = rc.OpenAPI.SpecID
					}
					if _, checkErr := g.schemaChecker.CheckAndStore(specID, doc); checkErr != nil {
						result.Error = checkErr.Error()
						return result
					}
				}
			}
		}
	}

	// Compute changes
	result.Changes = diffConfig(g.config, newCfg)

	// Save old state for cleanup
	oldWatchCancels := g.watchCancels
	oldManagers := g.routeManagers
	oldLoadShedder := g.loadShedder

	// Swap all state under write lock
	g.mu.Lock()
	g.config = newState.config
	g.router = newState.router
	g.routeProxies.Store(&newState.routeProxies)
	g.routeHandlers.Store(&newState.routeHandlers)
	g.watchCancels = newState.watchCancels
	g.features = newState.features
	g.routeManagers = newState.routeManagers
	// Rebuild global singletons from new config
	if newCfg.ServiceRateLimit.Enabled {
		g.serviceLimiter = serviceratelimit.New(newCfg.ServiceRateLimit)
	} else {
		g.serviceLimiter = nil
	}
	if newCfg.DebugEndpoint.Enabled {
		g.debugHandler = debug.New(newCfg.DebugEndpoint, newCfg)
	} else {
		g.debugHandler = nil
	}
	// Rebuild HTTPS redirect and allowed hosts from new config
	if newCfg.HTTPSRedirect.Enabled {
		g.httpsRedirect = httpsredirect.New(newCfg.HTTPSRedirect)
	} else {
		g.httpsRedirect = nil
	}
	if newCfg.AllowedHosts.Enabled {
		g.allowedHosts = allowedhosts.New(newCfg.AllowedHosts)
	} else {
		g.allowedHosts = nil
	}
	if newCfg.LoadShedding.Enabled {
		g.loadShedder = loadshed.New(newCfg.LoadShedding)
	} else {
		g.loadShedder = nil
	}
	g.mu.Unlock()

	// Clean up old state (after releasing lock — in-flight requests already hold handler refs)
	for _, cancel := range oldWatchCancels {
		cancel()
	}
	oldManagers.cleanup()
	if oldLoadShedder != nil {
		oldLoadShedder.Close()
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

	// Notify external features that support hot reload
	for _, ef := range g.externalFeatures {
		if rc, ok := ef.Feature.(ExternalReconfigurable); ok {
			if err := rc.Reconfigure(newCfg); err != nil {
				logging.Error("external feature reconfigure failed",
					zap.String("feature", ef.Feature.Name()),
					zap.Error(err))
			}
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

// wsProxy accessor for buildState — uses shared wsProxy from Runway
func (g *Runway) getWSProxy() *websocket.Proxy {
	return g.wsProxy
}

// resolver accessor for buildState
func (g *Runway) getResolver() *variables.Resolver {
	return g.resolver
}
