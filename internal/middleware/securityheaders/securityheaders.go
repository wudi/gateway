package securityheaders

import (
	"net/http"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
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
// Per-route non-empty fields override global; if per-route has no overrides it
// inherits the global value.
func MergeSecurityHeadersConfig(perRoute, global config.SecurityHeadersConfig) config.SecurityHeadersConfig {
	merged := global
	merged.Enabled = true // caller already decided to merge
	if perRoute.StrictTransportSecurity != "" {
		merged.StrictTransportSecurity = perRoute.StrictTransportSecurity
	}
	if perRoute.ContentSecurityPolicy != "" {
		merged.ContentSecurityPolicy = perRoute.ContentSecurityPolicy
	}
	if perRoute.XContentTypeOptions != "" {
		merged.XContentTypeOptions = perRoute.XContentTypeOptions
	}
	if perRoute.XFrameOptions != "" {
		merged.XFrameOptions = perRoute.XFrameOptions
	}
	if perRoute.ReferrerPolicy != "" {
		merged.ReferrerPolicy = perRoute.ReferrerPolicy
	}
	if perRoute.PermissionsPolicy != "" {
		merged.PermissionsPolicy = perRoute.PermissionsPolicy
	}
	if perRoute.CrossOriginOpenerPolicy != "" {
		merged.CrossOriginOpenerPolicy = perRoute.CrossOriginOpenerPolicy
	}
	if perRoute.CrossOriginEmbedderPolicy != "" {
		merged.CrossOriginEmbedderPolicy = perRoute.CrossOriginEmbedderPolicy
	}
	if perRoute.CrossOriginResourcePolicy != "" {
		merged.CrossOriginResourcePolicy = perRoute.CrossOriginResourcePolicy
	}
	if perRoute.XPermittedCrossDomainPolicies != "" {
		merged.XPermittedCrossDomainPolicies = perRoute.XPermittedCrossDomainPolicies
	}
	if len(perRoute.CustomHeaders) > 0 {
		merged.CustomHeaders = make(map[string]string)
		for k, v := range global.CustomHeaders {
			merged.CustomHeaders[k] = v
		}
		for k, v := range perRoute.CustomHeaders {
			merged.CustomHeaders[k] = v
		}
	}
	return merged
}

// SecurityHeadersByRoute is a ByRoute manager for per-route security headers.
type SecurityHeadersByRoute struct {
	byroute.Manager[*CompiledSecurityHeaders]
}

// NewSecurityHeadersByRoute creates a new manager.
func NewSecurityHeadersByRoute() *SecurityHeadersByRoute {
	return &SecurityHeadersByRoute{}
}

// AddRoute adds compiled security headers for a route.
func (m *SecurityHeadersByRoute) AddRoute(routeID string, cfg config.SecurityHeadersConfig) {
	m.Add(routeID, New(cfg))
}

// GetHeaders returns the compiled security headers for a route, or nil.
func (m *SecurityHeadersByRoute) GetHeaders(routeID string) *CompiledSecurityHeaders {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route snapshots.
func (m *SecurityHeadersByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&m.Manager, func(h *CompiledSecurityHeaders) Snapshot { return h.Snapshot() })
}
