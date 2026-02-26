package tenant

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/middleware/quota"
	"github.com/wudi/runway/variables"
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

// WithContext stores TenantInfo in a context.
func WithContext(ctx context.Context, info *TenantInfo) context.Context {
	return context.WithValue(ctx, contextKey{}, info)
}

// tenantState holds the immutable tenant map snapshot. Swapped atomically for lock-free reads.
type tenantState struct {
	tenants map[string]config.TenantConfig
}

// Manager handles tenant resolution, rate limiting, and quota enforcement.
type Manager struct {
	keyFn         func(*http.Request) string
	state         atomic.Pointer[tenantState] // lock-free read on hot path
	defaultTenant string
	rateLimiters  map[string]*rate.Limiter
	quotaEnforcers map[string]*quota.QuotaEnforcer
	mu            sync.RWMutex   // protects rateLimiters + quotaEnforcers
	writeMu       sync.Mutex     // serializes CUD operations
	redisClient   *redis.Client  // for quota enforcers in CRUD
	tiers         map[string]config.TenantTierConfig

	allowed  atomic.Int64
	rejected atomic.Int64

	// Per-tenant counters
	tenantAllowed       map[string]*atomic.Int64
	tenantRejected      map[string]*atomic.Int64
	tenantRateLimited   map[string]*atomic.Int64
	tenantQuotaExceeded map[string]*atomic.Int64

	// Per-tenant usage analytics
	tenantMetrics map[string]*TenantMetrics
}

// NewManager creates a tenant manager from configuration.
func NewManager(cfg config.TenantsConfig, redisClient *redis.Client) *Manager {
	// Merge tier defaults into tenant configs
	tenants := make(map[string]config.TenantConfig, len(cfg.Tenants))
	for name, tc := range cfg.Tenants {
		if tc.Tier != "" {
			if tier, ok := cfg.Tiers[tc.Tier]; ok {
				tc = mergeTenantWithTier(tc, tier)
			}
		}
		tenants[name] = tc
	}

	m := &Manager{
		keyFn:               buildKeyFunc(cfg.Key),
		defaultTenant:       cfg.DefaultTenant,
		rateLimiters:        make(map[string]*rate.Limiter),
		quotaEnforcers:      make(map[string]*quota.QuotaEnforcer),
		tenantAllowed:       make(map[string]*atomic.Int64),
		tenantRejected:      make(map[string]*atomic.Int64),
		tenantRateLimited:   make(map[string]*atomic.Int64),
		tenantQuotaExceeded: make(map[string]*atomic.Int64),
		tenantMetrics:       make(map[string]*TenantMetrics),
		redisClient:         redisClient,
		tiers:               cfg.Tiers,
	}
	m.state.Store(&tenantState{tenants: tenants})

	for name, tc := range tenants {
		m.tenantAllowed[name] = &atomic.Int64{}
		m.tenantRejected[name] = &atomic.Int64{}
		m.tenantRateLimited[name] = &atomic.Int64{}
		m.tenantQuotaExceeded[name] = &atomic.Int64{}
		m.tenantMetrics[name] = &TenantMetrics{}

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

			// Look up tenant config (lock-free read via atomic pointer)
			tc, found := m.state.Load().tenants[tenantID]
			if !found {
				if m.defaultTenant != "" {
					tenantID = m.defaultTenant
					tc = m.state.Load().tenants[tenantID]
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

			// Set TenantID on variable context for logging / variable resolution
			if varCtx := variables.GetFromRequest(r); varCtx != nil {
				varCtx.TenantID = tenantID
			}

			// Per-tenant timeout: context.WithTimeout naturally uses the lesser of
			// the route-level and tenant-level deadlines.
			if tc.Timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.Timeout)
				defer cancel()
				r = r.WithContext(ctx)
			}

			// Set response header
			w.Header().Set("X-Tenant-ID", tenantID)

			// Set per-tenant custom response headers
			for k, v := range tc.ResponseHeaders {
				w.Header().Set(k, v)
			}

			// Propagate metadata as request headers for backends
			for k, v := range tc.Metadata {
				r.Header.Set("X-Tenant-"+k, v)
			}

			// Wrap response writer for tenant usage analytics (conditional per architecture rules)
			if metrics := m.tenantMetrics[tenantID]; metrics != nil {
				tw := &tenantResponseWriter{ResponseWriter: w, status: 200}
				start := time.Now()
				next.ServeHTTP(tw, r)
				metrics.Record(tw.status, time.Since(start), r.ContentLength, tw.bytes)
				return
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
	for name := range m.state.Load().tenants {
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
		if metrics := m.tenantMetrics[name]; metrics != nil {
			ts["analytics"] = metrics.Snapshot()
		}
		tenantStats[name] = ts
	}
	return map[string]interface{}{
		"enabled":      true,
		"tenant_count": len(m.state.Load().tenants),
		"tenants":      tenantStats,
	}
}

// Close stops all background goroutines (quota cleanup).
func (m *Manager) Close() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, qe := range m.quotaEnforcers {
		qe.Close()
	}
}

// GetTenant returns the config for a tenant.
func (m *Manager) GetTenant(id string) (config.TenantConfig, bool) {
	tc, ok := m.state.Load().tenants[id]
	return tc, ok
}

// ListTenants returns a copy of all tenant configs.
func (m *Manager) ListTenants() map[string]config.TenantConfig {
	return m.state.Load().tenants
}

// AddTenant creates a new tenant at runtime. Returns an error if the tenant already exists.
func (m *Manager) AddTenant(id string, cfg config.TenantConfig) error {
	if id == "" {
		return fmt.Errorf("tenant ID is required")
	}

	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	current := m.state.Load().tenants
	if _, exists := current[id]; exists {
		return fmt.Errorf("tenant %q already exists", id)
	}

	// Merge tier if specified
	if cfg.Tier != "" {
		if tier, ok := m.tiers[cfg.Tier]; ok {
			cfg = mergeTenantWithTier(cfg, tier)
		}
	}

	// Copy-on-write
	newMap := make(map[string]config.TenantConfig, len(current)+1)
	for k, v := range current {
		newMap[k] = v
	}
	newMap[id] = cfg
	m.state.Store(&tenantState{tenants: newMap})

	// Initialize counters and resources
	m.tenantAllowed[id] = &atomic.Int64{}
	m.tenantRejected[id] = &atomic.Int64{}
	m.tenantRateLimited[id] = &atomic.Int64{}
	m.tenantQuotaExceeded[id] = &atomic.Int64{}
	m.tenantMetrics[id] = &TenantMetrics{}

	m.mu.Lock()
	m.initTenantResources(id, cfg)
	m.mu.Unlock()

	return nil
}

// UpdateTenant updates an existing tenant. Returns an error if the tenant doesn't exist.
func (m *Manager) UpdateTenant(id string, cfg config.TenantConfig) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	current := m.state.Load().tenants
	if _, exists := current[id]; !exists {
		return fmt.Errorf("tenant %q not found", id)
	}

	// Merge tier if specified
	if cfg.Tier != "" {
		if tier, ok := m.tiers[cfg.Tier]; ok {
			cfg = mergeTenantWithTier(cfg, tier)
		}
	}

	// Copy-on-write
	newMap := make(map[string]config.TenantConfig, len(current))
	for k, v := range current {
		newMap[k] = v
	}
	newMap[id] = cfg
	m.state.Store(&tenantState{tenants: newMap})

	// Recreate rate limiter and quota
	m.mu.Lock()
	// Close old quota enforcer
	if qe, ok := m.quotaEnforcers[id]; ok {
		qe.Close()
		delete(m.quotaEnforcers, id)
	}
	delete(m.rateLimiters, id)
	m.initTenantResources(id, cfg)
	m.mu.Unlock()

	return nil
}

// RemoveTenant removes a tenant at runtime. Returns an error if the tenant doesn't exist.
func (m *Manager) RemoveTenant(id string) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	current := m.state.Load().tenants
	if _, exists := current[id]; !exists {
		return fmt.Errorf("tenant %q not found", id)
	}

	// Copy-on-write
	newMap := make(map[string]config.TenantConfig, len(current)-1)
	for k, v := range current {
		if k != id {
			newMap[k] = v
		}
	}
	m.state.Store(&tenantState{tenants: newMap})

	// Clean up resources
	m.mu.Lock()
	if qe, ok := m.quotaEnforcers[id]; ok {
		qe.Close()
		delete(m.quotaEnforcers, id)
	}
	delete(m.rateLimiters, id)
	m.mu.Unlock()

	return nil
}

// initTenantResources sets up rate limiter and quota enforcer for a tenant.
// Caller must hold m.mu write lock.
func (m *Manager) initTenantResources(id string, tc config.TenantConfig) {
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
		m.rateLimiters[id] = rate.NewLimiter(r, burst)
	}
	if tc.Quota != nil {
		qcfg := config.QuotaConfig{
			Enabled: true,
			Limit:   tc.Quota.Limit,
			Period:  tc.Quota.Period,
			Key:     "ip",
		}
		m.quotaEnforcers[id] = quota.New("tenant:"+id, qcfg, m.redisClient)
	}
}

// mergeTenantWithTier merges tier defaults into a tenant config.
// Tenant-specific non-zero values override tier defaults.
// For maps (metadata, response_headers), values are merged with tenant keys winning.
func mergeTenantWithTier(tc config.TenantConfig, tier config.TenantTierConfig) config.TenantConfig {
	if tc.RateLimit == nil && tier.RateLimit != nil {
		tc.RateLimit = tier.RateLimit
	}
	if tc.Quota == nil && tier.Quota != nil {
		tc.Quota = tier.Quota
	}
	if tc.MaxBodySize == 0 && tier.MaxBodySize > 0 {
		tc.MaxBodySize = tier.MaxBodySize
	}
	if tc.Priority == 0 && tier.Priority > 0 {
		tc.Priority = tier.Priority
	}
	if tc.Timeout == 0 && tier.Timeout > 0 {
		tc.Timeout = tier.Timeout
	}

	// Merge metadata: tier defaults with tenant overrides
	if len(tier.Metadata) > 0 {
		merged := make(map[string]string, len(tier.Metadata)+len(tc.Metadata))
		for k, v := range tier.Metadata {
			merged[k] = v
		}
		for k, v := range tc.Metadata {
			merged[k] = v
		}
		tc.Metadata = merged
	}

	// Merge response headers: tier defaults with tenant overrides
	if len(tier.ResponseHeaders) > 0 {
		merged := make(map[string]string, len(tier.ResponseHeaders)+len(tc.ResponseHeaders))
		for k, v := range tier.ResponseHeaders {
			merged[k] = v
		}
		for k, v := range tc.ResponseHeaders {
			merged[k] = v
		}
		tc.ResponseHeaders = merged
	}

	return tc
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
