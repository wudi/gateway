package tenant

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/middleware/quota"
	"github.com/wudi/gateway/internal/variables"
	"golang.org/x/time/rate"
)

type contextKey struct{}

// TenantInfo contains resolved tenant data stored in request context.
type TenantInfo struct {
	ID       string
	Config   config.TenantConfig
	Metadata map[string]string
}

// FromContext retrieves the TenantInfo from a request context.
func FromContext(ctx context.Context) *TenantInfo {
	v, _ := ctx.Value(contextKey{}).(*TenantInfo)
	return v
}

// Manager handles tenant resolution, rate limiting, and quota enforcement.
type Manager struct {
	keyFn         func(*http.Request) string
	tenants       map[string]config.TenantConfig
	defaultTenant string
	rateLimiters  map[string]*rate.Limiter
	quotaEnforcers map[string]*quota.QuotaEnforcer
	mu            sync.RWMutex

	allowed  atomic.Int64
	rejected atomic.Int64

	// Per-tenant counters
	tenantAllowed    map[string]*atomic.Int64
	tenantRejected   map[string]*atomic.Int64
	tenantRateLimited map[string]*atomic.Int64
	tenantQuotaExceeded map[string]*atomic.Int64
}

// NewManager creates a tenant manager from configuration.
func NewManager(cfg config.TenantsConfig, redisClient *redis.Client) *Manager {
	m := &Manager{
		keyFn:               buildKeyFunc(cfg.Key),
		tenants:             cfg.Tenants,
		defaultTenant:       cfg.DefaultTenant,
		rateLimiters:        make(map[string]*rate.Limiter),
		quotaEnforcers:      make(map[string]*quota.QuotaEnforcer),
		tenantAllowed:       make(map[string]*atomic.Int64),
		tenantRejected:      make(map[string]*atomic.Int64),
		tenantRateLimited:   make(map[string]*atomic.Int64),
		tenantQuotaExceeded: make(map[string]*atomic.Int64),
	}

	for name, tc := range cfg.Tenants {
		m.tenantAllowed[name] = &atomic.Int64{}
		m.tenantRejected[name] = &atomic.Int64{}
		m.tenantRateLimited[name] = &atomic.Int64{}
		m.tenantQuotaExceeded[name] = &atomic.Int64{}

		if tc.RateLimit != nil {
			period := tc.RateLimit.Period
			if period <= 0 {
				period = time.Second
			}
			r := rate.Limit(float64(tc.RateLimit.Rate) / period.Seconds())
			burst := tc.RateLimit.Burst
			if burst <= 0 {
				burst = tc.RateLimit.Rate
			}
			m.rateLimiters[name] = rate.NewLimiter(r, burst)
		}

		if tc.Quota != nil {
			qcfg := config.QuotaConfig{
				Enabled: true,
				Limit:   tc.Quota.Limit,
				Period:  tc.Quota.Period,
				Key:     "ip", // placeholder; tenant quota is per-tenant, not per-client
			}
			m.quotaEnforcers[name] = quota.New("tenant:"+name, qcfg, redisClient)
		}
	}

	return m
}

// Middleware returns a middleware that resolves tenant and enforces policies.
func (m *Manager) Middleware(routeAllowed []string, routeRequired bool) middleware.Middleware {
	// Pre-compute allowed set for O(1) lookup
	var allowedSet map[string]bool
	if len(routeAllowed) > 0 {
		allowedSet = make(map[string]bool, len(routeAllowed))
		for _, id := range routeAllowed {
			allowedSet[id] = true
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID := m.keyFn(r)

			// If no tenant identifier found
			if tenantID == "" {
				if m.defaultTenant != "" {
					tenantID = m.defaultTenant
				} else if routeRequired {
					m.rejected.Add(1)
					http.Error(w, "Unknown tenant", http.StatusForbidden)
					return
				} else {
					// Not required — pass through without tenant context
					next.ServeHTTP(w, r)
					return
				}
			}

			// Look up tenant config
			tc, found := m.tenants[tenantID]
			if !found {
				if m.defaultTenant != "" {
					tenantID = m.defaultTenant
					tc = m.tenants[tenantID]
				} else if routeRequired {
					m.rejected.Add(1)
					http.Error(w, "Unknown tenant", http.StatusForbidden)
					return
				} else {
					next.ServeHTTP(w, r)
					return
				}
			}

			// Route ACL: check tenant's allowed routes
			if len(tc.Routes) > 0 {
				routeID := r.URL.Path // fallback
				// Use the route ID from variable context if available
				if vars := variables.GetFromRequest(r); vars != nil && vars.RouteID != "" {
					routeID = vars.RouteID
				}
				routeAllowedByTenant := false
				for _, rid := range tc.Routes {
					if rid == routeID {
						routeAllowedByTenant = true
						break
					}
				}
				if !routeAllowedByTenant {
					m.rejected.Add(1)
					if c := m.tenantRejected[tenantID]; c != nil {
						c.Add(1)
					}
					http.Error(w, "Tenant not authorized for this route", http.StatusForbidden)
					return
				}
			}

			// Route ACL: check route's allowed tenants
			if allowedSet != nil && !allowedSet[tenantID] {
				m.rejected.Add(1)
				if c := m.tenantRejected[tenantID]; c != nil {
					c.Add(1)
				}
				http.Error(w, "Tenant not authorized for this route", http.StatusForbidden)
				return
			}

			// Per-tenant rate limit
			if limiter, ok := m.rateLimiters[tenantID]; ok {
				if !limiter.Allow() {
					m.rejected.Add(1)
					if c := m.tenantRateLimited[tenantID]; c != nil {
						c.Add(1)
					}
					w.Header().Set("Retry-After", "1")
					http.Error(w, "Tenant rate limit exceeded", http.StatusTooManyRequests)
					return
				}
			}

			// Per-tenant quota (using a simple counter approach)
			if qe, ok := m.quotaEnforcers[tenantID]; ok {
				// Build a synthetic request with tenant ID as the key for quota tracking
				qr := r.Clone(r.Context())
				qr.RemoteAddr = tenantID + ":0"
				qw := &quotaCheckWriter{header: make(http.Header)}
				qe.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(qw, qr)
				if qw.code == http.StatusTooManyRequests {
					m.rejected.Add(1)
					if c := m.tenantQuotaExceeded[tenantID]; c != nil {
						c.Add(1)
					}
					w.Header().Set("Retry-After", qw.header.Get("Retry-After"))
					http.Error(w, "Tenant quota exceeded", http.StatusTooManyRequests)
					return
				}
			}

			m.allowed.Add(1)
			if c := m.tenantAllowed[tenantID]; c != nil {
				c.Add(1)
			}

			// Store tenant info in context
			info := &TenantInfo{
				ID:       tenantID,
				Config:   tc,
				Metadata: tc.Metadata,
			}
			ctx := context.WithValue(r.Context(), contextKey{}, info)
			r = r.WithContext(ctx)

			// Set response header
			w.Header().Set("X-Tenant-ID", tenantID)

			// Propagate metadata as request headers for backends
			for k, v := range tc.Metadata {
				r.Header.Set("X-Tenant-"+k, v)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// quotaCheckWriter is a minimal ResponseWriter for checking quota without sending a response.
type quotaCheckWriter struct {
	header http.Header
	code   int
}

func (qw *quotaCheckWriter) Header() http.Header       { return qw.header }
func (qw *quotaCheckWriter) WriteHeader(code int)       { qw.code = code }
func (qw *quotaCheckWriter) Write(b []byte) (int, error) { return len(b), nil }

// Stats returns per-tenant statistics for the admin API.
func (m *Manager) Stats() map[string]interface{} {
	tenantStats := make(map[string]interface{})
	for name := range m.tenants {
		ts := map[string]interface{}{
			"allowed":        int64(0),
			"rejected":       int64(0),
			"rate_limited":   int64(0),
			"quota_exceeded": int64(0),
		}
		if c := m.tenantAllowed[name]; c != nil {
			ts["allowed"] = c.Load()
		}
		if c := m.tenantRejected[name]; c != nil {
			ts["rejected"] = c.Load()
		}
		if c := m.tenantRateLimited[name]; c != nil {
			ts["rate_limited"] = c.Load()
		}
		if c := m.tenantQuotaExceeded[name]; c != nil {
			ts["quota_exceeded"] = c.Load()
		}
		tenantStats[name] = ts
	}
	return map[string]interface{}{
		"enabled":      true,
		"tenant_count": len(m.tenants),
		"tenants":      tenantStats,
	}
}

// Close stops all background goroutines (quota cleanup).
func (m *Manager) Close() {
	for _, qe := range m.quotaEnforcers {
		qe.Close()
	}
}

func buildKeyFunc(key string) func(*http.Request) string {
	switch {
	case key == "client_id":
		return func(r *http.Request) string {
			if vars := variables.GetFromRequest(r); vars != nil && vars.Identity.ClientID != "" {
				return vars.Identity.ClientID
			}
			return ""
		}
	case strings.HasPrefix(key, "header:"):
		headerName := key[len("header:"):]
		return func(r *http.Request) string {
			return r.Header.Get(headerName)
		}
	case strings.HasPrefix(key, "jwt_claim:"):
		claimName := key[len("jwt_claim:"):]
		return func(r *http.Request) string {
			if vars := variables.GetFromRequest(r); vars != nil {
				if val, ok := vars.Identity.Claims[claimName]; ok {
					switch v := val.(type) {
					case string:
						return v
					case float64:
						return strconv.FormatFloat(v, 'f', -1, 64)
					default:
						return ""
					}
				}
			}
			return ""
		}
	default:
		return func(r *http.Request) string { return "" }
	}
}
