package router

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/julienschmidt/httprouter"
	"github.com/wudi/gateway/internal/config"
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
	UpstreamName   string
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
	MaxBodySize    int64
	IPFilter       config.IPFilterConfig
	CORS           config.CORSConfig
	Compression    config.CompressionConfig
	TrafficSplit   []config.TrafficSplitConfig
	Validation     config.ValidationConfig
	Mirror         config.MirrorConfig
	GRPC           config.GRPCConfig
	MatchCfg       config.MatchConfig
	Rewrite          config.RewriteConfig
	FollowRedirects  config.FollowRedirectsConfig
	Echo             bool

	rewriteRegex *regexp.Regexp // compiled regex for rewrite (nil if no regex rewrite)
	matcher      *CompiledMatcher
	configIdx    int // insertion order for tie-breaking
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

// RouteGroup holds an ordered slice of candidate routes sharing a path pattern.
// Routes are sorted by specificity (descending), with config insertion order as tie-breaker.
type RouteGroup struct {
	routes []*Route
}

// ServeHTTP is called by httprouter for a matched path. It type-asserts the writer
// to *captureWriter, iterates candidates, and stores the first matching route.
func (rg *RouteGroup) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	cw, ok := w.(*captureWriter)
	if !ok {
		return
	}

	// Extract httprouter params from request context
	params := httprouter.ParamsFromContext(r.Context())
	pathParams := make(map[string]string, len(params))
	for _, p := range params {
		pathParams[p.Key] = p.Value
	}

	for _, route := range rg.routes {
		if route.matcher.Matches(r) {
			cw.match = &Match{
				Route:      route,
				PathParams: pathParams,
			}
			return
		}
	}
}

// addRoute adds a route to the group and re-sorts by specificity.
func (rg *RouteGroup) addRoute(route *Route) {
	rg.routes = append(rg.routes, route)
	sort.SliceStable(rg.routes, func(i, j int) bool {
		si := rg.routes[i].matcher.Specificity()
		sj := rg.routes[j].matcher.Specificity()
		if si != sj {
			return si > sj
		}
		return rg.routes[i].configIdx < rg.routes[j].configIdx
	})
}

// removeRoute removes a route by ID from the group. Returns true if found.
func (rg *RouteGroup) removeRoute(id string) bool {
	for i, route := range rg.routes {
		if route.ID == id {
			rg.routes = append(rg.routes[:i], rg.routes[i+1:]...)
			return true
		}
	}
	return false
}

// captureWriter is a no-op ResponseWriter used to extract the match result
// from httprouter dispatch without writing any actual HTTP response.
type captureWriter struct {
	match  *Match
	header http.Header
}

func newCaptureWriter() *captureWriter {
	return &captureWriter{header: make(http.Header)}
}

func (cw *captureWriter) Header() http.Header       { return cw.header }
func (cw *captureWriter) Write([]byte) (int, error) { return 0, nil }
func (cw *captureWriter) WriteHeader(int)           {}

// HasRewriteRegex returns true if this route has a compiled regex rewrite pattern.
func (route *Route) HasRewriteRegex() bool {
	return route.rewriteRegex != nil
}

// SetRewriteRegex compiles and sets the rewrite regex from the given pattern.
func (route *Route) SetRewriteRegex(pattern string) {
	route.rewriteRegex = regexp.MustCompile(pattern)
}

// RewritePath applies the route's rewrite rules to transform the request path.
// For prefix rewrite: strips the route prefix and prepends the rewrite prefix.
// For regex rewrite: applies regex substitution on the full request path.
// Returns the path unchanged if no rewrite is configured.
func (route *Route) RewritePath(requestPath string) string {
	if route.Rewrite.Prefix != "" {
		// Strip route prefix from request path, prepend rewrite prefix
		suffix := stripRoutePrefix(route.Path, requestPath)
		return singleJoinSlash(route.Rewrite.Prefix, suffix)
	}
	if route.rewriteRegex != nil {
		return route.rewriteRegex.ReplaceAllString(requestPath, route.Rewrite.Replacement)
	}
	return requestPath
}

// stripRoutePrefix removes the route's path prefix from the request path.
func stripRoutePrefix(pattern, path string) string {
	pattern = strings.Trim(pattern, "/")
	path = strings.Trim(path, "/")

	if pattern == "" {
		return "/" + path
	}

	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(pathParts) <= len(patternParts) {
		return "/"
	}

	suffix := strings.Join(pathParts[len(patternParts):], "/")
	if suffix == "" {
		return "/"
	}
	return "/" + suffix
}

// singleJoinSlash joins two URL path segments with exactly one slash.
func singleJoinSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// prefixRoute holds a prefix route with its compiled segments for matching.
type prefixRoute struct {
	route    *Route
	segments []string // pre-split normalized segments (no params)
	group    *RouteGroup
}

// Router handles path-based HTTP routing using httprouter for tier-1 path matching
// and RouteGroup for tier-2 domain/header/query matching.
type Router struct {
	tree            *httprouter.Router
	groups          map[string]*RouteGroup // normalized path → group (exact routes only)
	prefixGroups    []*prefixRoute         // prefix routes checked as fallback
	prefixByPath    map[string]*RouteGroup // normalized prefix path → group
	allRoutes       []*Route
	mu              sync.RWMutex
	notFound        http.Handler
	nextIdx         int
	registeredPaths map[string]bool // tracks method+path combos registered with httprouter
}

// standardMethods lists HTTP methods registered with httprouter for each path.
var standardMethods = []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}

// New creates a new router
func New() *Router {
	tree := httprouter.New()
	tree.HandleMethodNotAllowed = false
	tree.RedirectTrailingSlash = false
	tree.RedirectFixedPath = false

	return &Router{
		tree:            tree,
		groups:          make(map[string]*RouteGroup),
		prefixByPath:    make(map[string]*RouteGroup),
		registeredPaths: make(map[string]bool),
		notFound: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Not Found", http.StatusNotFound)
		}),
	}
}

// AddRoute adds a route to the router
func (rt *Router) AddRoute(routeCfg config.RouteConfig) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

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
		MaxBodySize:    routeCfg.MaxBodySize,
		IPFilter:       routeCfg.IPFilter,
		CORS:           routeCfg.CORS,
		Compression:    routeCfg.Compression,
		TrafficSplit:   routeCfg.TrafficSplit,
		Validation:     routeCfg.Validation,
		Mirror:         routeCfg.Mirror,
		GRPC:           routeCfg.GRPC,
		MatchCfg:       routeCfg.Match,
		Rewrite:          routeCfg.Rewrite,
		FollowRedirects:  routeCfg.FollowRedirects,
		Echo:             routeCfg.Echo,
		configIdx:      rt.nextIdx,
	}
	rt.nextIdx++

	// Compile rewrite regex if configured
	if routeCfg.Rewrite.Regex != "" {
		route.rewriteRegex = regexp.MustCompile(routeCfg.Rewrite.Regex)
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
		route.Methods = nil // nil = all methods
	} else {
		for _, m := range routeCfg.Methods {
			route.Methods[strings.ToUpper(m)] = true
		}
	}

	// Create compiled matcher for domain/header/query/method
	route.matcher = NewCompiledMatcher(routeCfg.Match, routeCfg.Methods)

	if routeCfg.PathPrefix {
		rt.addPrefixRoute(route, routeCfg.Path)
	} else {
		rt.addExactRoute(route, routeCfg.Path)
	}

	rt.allRoutes = append(rt.allRoutes, route)
	return nil
}

// addExactRoute registers an exact (non-prefix) route with httprouter.
func (rt *Router) addExactRoute(route *Route, path string) {
	normalized := replaceParams(path)
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}

	group, exists := rt.groups[normalized]
	if !exists {
		group = &RouteGroup{}
		rt.groups[normalized] = group

		for _, method := range standardMethods {
			key := method + " " + normalized
			if !rt.registeredPaths[key] {
				rt.tree.Handler(method, normalized, group)
				rt.registeredPaths[key] = true
			}
		}
	}

	group.addRoute(route)
}

// addPrefixRoute registers a prefix route. Prefix routes are matched separately
// from httprouter's radix tree to avoid catch-all parameter conflicts.
func (rt *Router) addPrefixRoute(route *Route, path string) {
	normalized := replaceParams(path)
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}

	// Also register the exact path in httprouter so the prefix base itself matches
	group, exists := rt.groups[normalized]
	if !exists {
		group = &RouteGroup{}
		rt.groups[normalized] = group

		for _, method := range standardMethods {
			key := method + " " + normalized
			if !rt.registeredPaths[key] {
				rt.tree.Handler(method, normalized, group)
				rt.registeredPaths[key] = true
			}
		}
	}
	group.addRoute(route)

	// Add to prefix groups for subpath matching
	prefixGroup, exists := rt.prefixByPath[normalized]
	if !exists {
		prefixGroup = &RouteGroup{}
		rt.prefixByPath[normalized] = prefixGroup

		segments := splitPath(normalized)
		rt.prefixGroups = append(rt.prefixGroups, &prefixRoute{
			route:    route,
			segments: segments,
			group:    prefixGroup,
		})

		// Sort prefix routes: longer paths first (more specific)
		sort.Slice(rt.prefixGroups, func(i, j int) bool {
			return len(rt.prefixGroups[i].segments) > len(rt.prefixGroups[j].segments)
		})
	}
	prefixGroup.addRoute(route)
}

// Match finds a route matching the request
func (rt *Router) Match(r *http.Request) *Match {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	// Tier 1: Try httprouter for exact/param paths
	cw := newCaptureWriter()
	rt.tree.ServeHTTP(cw, r)
	if cw.match != nil {
		return cw.match
	}

	// Tier 2: Try prefix routes for subpaths
	return rt.matchPrefix(r)
}

// matchPrefix checks prefix routes against the request path.
func (rt *Router) matchPrefix(r *http.Request) *Match {
	reqPath := r.URL.Path
	reqSegments := splitPath(reqPath)

	for _, pr := range rt.prefixGroups {
		if !pathHasPrefix(reqSegments, pr.segments) {
			continue
		}

		// Check each route in the group
		for _, route := range pr.group.routes {
			if route.matcher.Matches(r) {
				return &Match{
					Route:      route,
					PathParams: make(map[string]string),
				}
			}
		}
	}

	return nil
}

// GetRoute returns a route by ID
func (rt *Router) GetRoute(id string) *Route {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	for _, route := range rt.allRoutes {
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

	result := make([]*Route, len(rt.allRoutes))
	copy(result, rt.allRoutes)
	return result
}

// RemoveRoute removes a route by ID
func (rt *Router) RemoveRoute(id string) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	// Remove from allRoutes
	found := false
	for i, route := range rt.allRoutes {
		if route.ID == id {
			rt.allRoutes = append(rt.allRoutes[:i], rt.allRoutes[i+1:]...)
			found = true
			break
		}
	}

	if !found {
		return false
	}

	// Remove from exact groups
	for _, group := range rt.groups {
		group.removeRoute(id)
	}

	// Remove from prefix groups
	for _, group := range rt.prefixByPath {
		group.removeRoute(id)
	}

	return true
}

// UpdateBackends updates the backends for a route
func (rt *Router) UpdateBackends(routeID string, backends []Backend) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	for _, route := range rt.allRoutes {
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

// splitPath splits a URL path into non-empty segments.
func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// pathHasPrefix checks if reqSegments starts with prefixSegments.
func pathHasPrefix(reqSegments, prefixSegments []string) bool {
	if len(reqSegments) < len(prefixSegments) {
		return false
	}
	for i, seg := range prefixSegments {
		// Skip param segments (start with ':')
		if strings.HasPrefix(seg, ":") {
			continue
		}
		if reqSegments[i] != seg {
			return false
		}
	}
	return true
}

// replaceParams converts {name} path parameters to :name httprouter syntax.
func replaceParams(path string) string {
	var result strings.Builder
	i := 0
	for i < len(path) {
		if path[i] == '{' {
			j := strings.IndexByte(path[i:], '}')
			if j == -1 {
				result.WriteByte(path[i])
				i++
				continue
			}
			paramName := path[i+1 : i+j]
			result.WriteByte(':')
			result.WriteString(paramName)
			i += j + 1
		} else {
			result.WriteByte(path[i])
			i++
		}
	}
	return result.String()
}
