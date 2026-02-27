package securityheaders

import (
	"net/http"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// headerPair is a pre-computed header name + value.
type headerPair struct {
	Name  string
	Value string
}

// CompiledSecurityHeaders holds pre-computed security headers for a route.
type CompiledSecurityHeaders struct {
	headers []headerPair
	metrics Metrics
}

// Metrics tracks security headers middleware statistics.
type Metrics struct {
	TotalRequests int64
}

// Snapshot is a point-in-time copy of metrics.
type Snapshot struct {
	TotalRequests int64  `json:"total_requests"`
	HeaderCount   int    `json:"header_count"`
	Headers       []string `json:"headers"`
}

// New creates a CompiledSecurityHeaders from config. Defaults are applied
// for fields not explicitly set.
func New(cfg config.SecurityHeadersConfig) *CompiledSecurityHeaders {
	var pairs []headerPair

	// Apply defaults for commonly-expected headers if not explicitly set
	xcto := cfg.XContentTypeOptions
	if xcto == "" {
		xcto = "nosniff"
	}
	pairs = append(pairs, headerPair{"X-Content-Type-Options", xcto})

	if cfg.StrictTransportSecurity != "" {
		pairs = append(pairs, headerPair{"Strict-Transport-Security", cfg.StrictTransportSecurity})
	}
	if cfg.ContentSecurityPolicy != "" {
		pairs = append(pairs, headerPair{"Content-Security-Policy", cfg.ContentSecurityPolicy})
	}
	if cfg.XFrameOptions != "" {
		pairs = append(pairs, headerPair{"X-Frame-Options", cfg.XFrameOptions})
	}
	if cfg.ReferrerPolicy != "" {
		pairs = append(pairs, headerPair{"Referrer-Policy", cfg.ReferrerPolicy})
	}
	if cfg.PermissionsPolicy != "" {
		pairs = append(pairs, headerPair{"Permissions-Policy", cfg.PermissionsPolicy})
	}
	if cfg.CrossOriginOpenerPolicy != "" {
		pairs = append(pairs, headerPair{"Cross-Origin-Opener-Policy", cfg.CrossOriginOpenerPolicy})
	}
	if cfg.CrossOriginEmbedderPolicy != "" {
		pairs = append(pairs, headerPair{"Cross-Origin-Embedder-Policy", cfg.CrossOriginEmbedderPolicy})
	}
	if cfg.CrossOriginResourcePolicy != "" {
		pairs = append(pairs, headerPair{"Cross-Origin-Resource-Policy", cfg.CrossOriginResourcePolicy})
	}
	if cfg.XPermittedCrossDomainPolicies != "" {
		pairs = append(pairs, headerPair{"X-Permitted-Cross-Domain-Policies", cfg.XPermittedCrossDomainPolicies})
	}
	for name, value := range cfg.CustomHeaders {
		pairs = append(pairs, headerPair{name, value})
	}

	return &CompiledSecurityHeaders{
		headers: pairs,
	}
}

// Apply sets all configured security headers on the response.
func (c *CompiledSecurityHeaders) Apply(h http.Header) {
	atomic.AddInt64(&c.metrics.TotalRequests, 1)
	for _, p := range c.headers {
		h.Set(p.Name, p.Value)
	}
}

// Snapshot returns a point-in-time copy of metrics.
func (c *CompiledSecurityHeaders) Snapshot() Snapshot {
	names := make([]string, len(c.headers))
	for i, p := range c.headers {
		names[i] = p.Name
	}
	return Snapshot{
		TotalRequests: atomic.LoadInt64(&c.metrics.TotalRequests),
		HeaderCount:   len(c.headers),
		Headers:       names,
	}
}

// MergeSecurityHeadersConfig merges per-route config over global config.
func MergeSecurityHeadersConfig(perRoute, global config.SecurityHeadersConfig) config.SecurityHeadersConfig {
	merged := config.MergeNonZero(global, perRoute)
	merged.Enabled = true
	return merged
}

// SecurityHeadersByRoute is a ByRoute manager for per-route security headers.
type SecurityHeadersByRoute = byroute.Factory[*CompiledSecurityHeaders, config.SecurityHeadersConfig]

// NewSecurityHeadersByRoute creates a new manager.
func NewSecurityHeadersByRoute() *SecurityHeadersByRoute {
	return byroute.SimpleFactory(New, func(h *CompiledSecurityHeaders) any { return h.Snapshot() })
}

// Middleware returns a middleware that injects configured security response headers.
func (sh *CompiledSecurityHeaders) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sh.Apply(w.Header())
			next.ServeHTTP(w, r)
		})
	}
}
