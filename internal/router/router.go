package router

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/example/gateway/internal/config"
)

// Route represents a configured route
type Route struct {
	ID             string
	Path           string
	PathPrefix     bool
	Methods        map[string]bool
	Backends       []Backend
	ServiceName    string
	ServiceTags    []string
	Auth           RouteAuth
	RateLimit      config.RateLimitConfig
	Transform      config.TransformConfig
	Timeout        int64
	Retries        int
	StripPrefix    bool
	RetryPolicy    config.RetryConfig
	TimeoutPolicy  config.TimeoutConfig
	CircuitBreaker config.CircuitBreakerConfig
	Cache          config.CacheConfig
	WebSocket      config.WebSocketConfig
	matcher        *Matcher
}

// Backend represents a backend server
type Backend struct {
	URL    string
	Weight int
}

// RouteAuth contains authentication settings for a route
type RouteAuth struct {
	Required bool
	Methods  []string
}

// Match represents a route match result
type Match struct {
	Route      *Route
	PathParams map[string]string
}

// Router handles path-based routing
type Router struct {
	routes   []*Route
	mu       sync.RWMutex
	notFound http.Handler
}

// New creates a new router
func New() *Router {
	return &Router{
		routes: make([]*Route, 0),
		notFound: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Not Found", http.StatusNotFound)
		}),
	}
}

// AddRoute adds a route to the router
func (rt *Router) AddRoute(routeCfg config.RouteConfig) error {
	route := &Route{
		ID:          routeCfg.ID,
		Path:        routeCfg.Path,
		PathPrefix:  routeCfg.PathPrefix,
		Methods:     make(map[string]bool),
		ServiceName: routeCfg.Service.Name,
		ServiceTags: routeCfg.Service.Tags,
		Auth: RouteAuth{
			Required: routeCfg.Auth.Required,
			Methods:  routeCfg.Auth.Methods,
		},
		RateLimit:      routeCfg.RateLimit,
		Transform:      routeCfg.Transform,
		Timeout:        int64(routeCfg.Timeout),
		Retries:        routeCfg.Retries,
		StripPrefix:    routeCfg.StripPrefix,
		RetryPolicy:    routeCfg.RetryPolicy,
		TimeoutPolicy:  routeCfg.TimeoutPolicy,
		CircuitBreaker: routeCfg.CircuitBreaker,
		Cache:          routeCfg.Cache,
		WebSocket:      routeCfg.WebSocket,
		matcher:        NewMatcher(routeCfg.Path, routeCfg.PathPrefix),
	}

	// Convert backends
	for _, b := range routeCfg.Backends {
		weight := b.Weight
		if weight == 0 {
			weight = 1
		}
		route.Backends = append(route.Backends, Backend{
			URL:    b.URL,
			Weight: weight,
		})
	}

	// Set allowed methods
	if len(routeCfg.Methods) == 0 {
		// Allow all methods if none specified
		route.Methods = nil
	} else {
		for _, m := range routeCfg.Methods {
			route.Methods[strings.ToUpper(m)] = true
		}
	}

	rt.mu.Lock()
	rt.routes = append(rt.routes, route)
	// Sort routes by specificity (exact matches first, then by path length)
	sort.Slice(rt.routes, func(i, j int) bool {
		// Exact matches before prefix matches
		if !rt.routes[i].PathPrefix && rt.routes[j].PathPrefix {
			return true
		}
		if rt.routes[i].PathPrefix && !rt.routes[j].PathPrefix {
			return false
		}
		// Longer paths first (more specific)
		return len(rt.routes[i].Path) > len(rt.routes[j].Path)
	})
	rt.mu.Unlock()

	return nil
}

// Match finds a route matching the request
func (rt *Router) Match(r *http.Request) *Match {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	path := r.URL.Path
	method := r.Method

	for _, route := range rt.routes {
		// Check if path matches
		params, ok := route.matcher.Match(path)
		if !ok {
			continue
		}

		// Check method if specified
		if route.Methods != nil && !route.Methods[method] {
			continue
		}

		return &Match{
			Route:      route,
			PathParams: params,
		}
	}

	return nil
}

// GetRoute returns a route by ID
func (rt *Router) GetRoute(id string) *Route {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	for _, route := range rt.routes {
		if route.ID == id {
			return route
		}
	}
	return nil
}

// GetRoutes returns all configured routes
func (rt *Router) GetRoutes() []*Route {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	result := make([]*Route, len(rt.routes))
	copy(result, rt.routes)
	return result
}

// RemoveRoute removes a route by ID
func (rt *Router) RemoveRoute(id string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	for i, route := range rt.routes {
		if route.ID == id {
			rt.routes = append(rt.routes[:i], rt.routes[i+1:]...)
			return true
		}
	}
	return false
}

// UpdateBackends updates the backends for a route
func (rt *Router) UpdateBackends(routeID string, backends []Backend) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	for _, route := range rt.routes {
		if route.ID == routeID {
			route.Backends = backends
			return true
		}
	}
	return false
}

// SetNotFoundHandler sets the handler for unmatched routes
func (rt *Router) SetNotFoundHandler(h http.Handler) {
	rt.notFound = h
}

// NotFoundHandler returns the not found handler
func (rt *Router) NotFoundHandler() http.Handler {
	return rt.notFound
}
