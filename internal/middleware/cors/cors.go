package cors

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/wudi/gateway/internal/config"
)

// Handler manages CORS for a route
type Handler struct {
	enabled             bool
	allowOrigins        []string
	allowOriginPatterns []*regexp.Regexp
	allowMethods        string
	allowHeaders        string
	exposeHeaders       string
	allowCredentials    bool
	allowPrivateNetwork bool
	maxAge              string
	allowAllOrigins     bool
}

// New creates a new CORS handler from config
func New(cfg config.CORSConfig) (*Handler, error) {
	h := &Handler{
		enabled:             cfg.Enabled,
		allowOrigins:        cfg.AllowOrigins,
		allowCredentials:    cfg.AllowCredentials,
		allowPrivateNetwork: cfg.AllowPrivateNetwork,
	}

	// Compile regex patterns
	for _, pattern := range cfg.AllowOriginPatterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		h.allowOriginPatterns = append(h.allowOriginPatterns, re)
	}

	if len(cfg.AllowMethods) > 0 {
		h.allowMethods = strings.Join(cfg.AllowMethods, ", ")
	} else {
		h.allowMethods = "GET, POST, PUT, DELETE, PATCH, OPTIONS"
	}

	if len(cfg.AllowHeaders) > 0 {
		h.allowHeaders = strings.Join(cfg.AllowHeaders, ", ")
	} else {
		h.allowHeaders = "Content-Type, Authorization, X-API-Key"
	}

	if len(cfg.ExposeHeaders) > 0 {
		h.exposeHeaders = strings.Join(cfg.ExposeHeaders, ", ")
	}

	if cfg.MaxAge > 0 {
		h.maxAge = strconv.Itoa(cfg.MaxAge)
	} else {
		h.maxAge = "86400"
	}

	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			h.allowAllOrigins = true
			break
		}
	}

	return h, nil
}

// IsEnabled returns whether CORS is enabled
func (h *Handler) IsEnabled() bool {
	return h.enabled
}

// IsPreflight returns true if the request is a CORS preflight
func (h *Handler) IsPreflight(r *http.Request) bool {
	return h.enabled && r.Method == http.MethodOptions && r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != ""
}

// HandlePreflight writes a 204 response with CORS headers for preflight requests
func (h *Handler) HandlePreflight(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if !h.isOriginAllowed(origin) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	respOrigin := origin
	if h.allowAllOrigins && !h.allowCredentials {
		respOrigin = "*"
	}

	w.Header().Set("Access-Control-Allow-Origin", respOrigin)
	w.Header().Set("Access-Control-Allow-Methods", h.allowMethods)
	w.Header().Set("Access-Control-Allow-Headers", h.allowHeaders)

	if h.allowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}

	if h.allowPrivateNetwork && r.Header.Get("Access-Control-Request-Private-Network") == "true" {
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}

	w.Header().Set("Access-Control-Max-Age", h.maxAge)
	w.Header().Set("Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
	w.WriteHeader(http.StatusNoContent)
}

// ApplyHeaders adds CORS headers to a normal (non-preflight) response
func (h *Handler) ApplyHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin == "" || !h.isOriginAllowed(origin) {
		return
	}

	respOrigin := origin
	if h.allowAllOrigins && !h.allowCredentials {
		respOrigin = "*"
	}

	w.Header().Set("Access-Control-Allow-Origin", respOrigin)

	if h.allowCredentials {
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}

	if h.exposeHeaders != "" {
		w.Header().Set("Access-Control-Expose-Headers", h.exposeHeaders)
	}

	w.Header().Set("Vary", "Origin")
}

func (h *Handler) isOriginAllowed(origin string) bool {
	if h.allowAllOrigins {
		return true
	}

	for _, allowed := range h.allowOrigins {
		if allowed == origin {
			return true
		}
		// Simple wildcard matching: *.example.com
		if strings.HasPrefix(allowed, "*.") {
			suffix := allowed[1:] // .example.com
			if strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}

	// Check regex patterns
	for _, re := range h.allowOriginPatterns {
		if re.MatchString(origin) {
			return true
		}
	}

	return false
}

// CORSByRoute manages CORS handlers per route
type CORSByRoute struct {
	handlers map[string]*Handler
	mu       sync.RWMutex
}

// NewCORSByRoute creates a new per-route CORS manager
func NewCORSByRoute() *CORSByRoute {
	return &CORSByRoute{
		handlers: make(map[string]*Handler),
	}
}

// AddRoute adds a CORS handler for a route
func (m *CORSByRoute) AddRoute(routeID string, cfg config.CORSConfig) error {
	h, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.handlers[routeID] = h
	m.mu.Unlock()
	return nil
}

// GetHandler returns the CORS handler for a route
func (m *CORSByRoute) GetHandler(routeID string) *Handler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.handlers[routeID]
}

// RouteIDs returns all route IDs with CORS handlers.
func (m *CORSByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.handlers))
	for id := range m.handlers {
		ids = append(ids, id)
	}
	return ids
}
