package geo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/variables"
	"go.uber.org/zap"
)

// geoResultKey is the context key for storing GeoResult.
type geoResultKey struct{}

// WithGeoResult stores a GeoResult in the request context.
func WithGeoResult(ctx context.Context, result *GeoResult) context.Context {
	return context.WithValue(ctx, geoResultKey{}, result)
}

// GeoResultFromContext retrieves the GeoResult from context, or nil if not present.
func GeoResultFromContext(ctx context.Context) *GeoResult {
	if v, ok := ctx.Value(geoResultKey{}).(*GeoResult); ok {
		return v
	}
	return nil
}

// CompiledGeo is a compiled per-route geo filter created once during route setup.
type CompiledGeo struct {
	provider       Provider
	allowCountries map[string]bool // uppercase ISO codes
	denyCountries  map[string]bool
	allowCities    map[string]bool // lowercase normalized
	denyCities     map[string]bool
	order          string // "allow_first" or "deny_first"
	injectHeaders  bool
	shadowMode     bool
	metrics        *GeoMetrics
	routeID        string
}

// New creates a new CompiledGeo from config and a shared Provider.
func New(routeID string, cfg config.GeoConfig, provider Provider) (*CompiledGeo, error) {
	order := cfg.Order
	if order == "" {
		order = "deny_first"
	}

	injectHeaders := cfg.InjectHeaders
	// Default inject_headers to true if not explicitly set (zero value for bool is false,
	// but we want true by default — plan says default true, so if not enabled we default to true)
	// Since bool zero is false, we treat the config as "inject headers unless explicitly set to false"
	// The plan says "default true" so we use the Enabled field convention.
	// Since Go bools default to false, we need the caller to set InjectHeaders: true.
	// Let the merge function handle this.

	g := &CompiledGeo{
		provider:       provider,
		allowCountries: make(map[string]bool),
		denyCountries:  make(map[string]bool),
		allowCities:    make(map[string]bool),
		denyCities:     make(map[string]bool),
		order:          order,
		injectHeaders:  injectHeaders,
		shadowMode:     cfg.ShadowMode,
		metrics:        &GeoMetrics{},
		routeID:        routeID,
	}

	for _, c := range cfg.AllowCountries {
		g.allowCountries[strings.ToUpper(c)] = true
	}
	for _, c := range cfg.DenyCountries {
		g.denyCountries[strings.ToUpper(c)] = true
	}
	for _, c := range cfg.AllowCities {
		g.allowCities[strings.ToLower(c)] = true
	}
	for _, c := range cfg.DenyCities {
		g.denyCities[strings.ToLower(c)] = true
	}

	return g, nil
}

// Handle performs the geo check for a request. Returns the (possibly modified) request
// and true if the request is allowed. The returned request has the GeoResult stored
// in its context for downstream use (e.g., rules engine). On deny it writes a 451 response.
func (g *CompiledGeo) Handle(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	g.metrics.TotalRequests.Add(1)

	clientIP := variables.ExtractClientIP(r)
	result, err := g.provider.Lookup(clientIP)
	if err != nil {
		g.metrics.LookupErrors.Add(1)
		logging.Warn("Geo lookup error",
			zap.String("route", g.routeID),
			zap.String("ip", clientIP),
			zap.Error(err),
		)
		// On lookup error, allow the request through
		g.metrics.Allowed.Add(1)
		return r, true
	}

	// Store geo result in request context for downstream middleware (e.g., rules engine)
	r = r.WithContext(WithGeoResult(r.Context(), result))

	// Inject geo headers if configured
	if g.injectHeaders {
		if result.CountryCode != "" {
			r.Header.Set("X-Geo-Country", result.CountryCode)
		}
		if result.City != "" {
			r.Header.Set("X-Geo-City", result.City)
		}
	}

	// Check allow/deny rules
	allowed := g.checkRules(result)

	if !allowed {
		if g.shadowMode {
			logging.Info("Geo filter would deny (shadow mode)",
				zap.String("route", g.routeID),
				zap.String("ip", clientIP),
				zap.String("country", result.CountryCode),
				zap.String("city", result.City),
			)
			g.metrics.Allowed.Add(1)
			return r, true
		}

		g.metrics.Denied.Add(1)
		logging.Info("Geo filter denied request",
			zap.String("route", g.routeID),
			zap.String("ip", clientIP),
			zap.String("country", result.CountryCode),
			zap.String("city", result.City),
		)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnavailableForLegalReasons)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":   "geo_restricted",
			"message": "Request blocked by geographic restriction",
			"status":  451,
		})
		return r, false
	}

	g.metrics.Allowed.Add(1)
	return r, true
}

// checkRules evaluates the allow/deny lists against the geo result.
func (g *CompiledGeo) checkRules(result *GeoResult) bool {
	countryUpper := strings.ToUpper(result.CountryCode)
	cityLower := strings.ToLower(result.City)

	hasAllowRules := len(g.allowCountries) > 0 || len(g.allowCities) > 0
	hasDenyRules := len(g.denyCountries) > 0 || len(g.denyCities) > 0

	// No rules → allow
	if !hasAllowRules && !hasDenyRules {
		return true
	}

	switch g.order {
	case "allow_first":
		// If allow lists exist and match → allow
		if hasAllowRules && g.matchesAllow(countryUpper, cityLower) {
			return true
		}
		// If deny lists exist and match → deny
		if hasDenyRules && g.matchesDeny(countryUpper, cityLower) {
			return false
		}
		// Otherwise allow
		return true

	default: // "deny_first"
		// If deny lists exist and match → deny
		if hasDenyRules && g.matchesDeny(countryUpper, cityLower) {
			return false
		}
		// If allow lists exist and NOT matched → deny
		if hasAllowRules && !g.matchesAllow(countryUpper, cityLower) {
			return false
		}
		// Otherwise allow
		return true
	}
}

// matchesAllow checks if the result matches any allow rule.
func (g *CompiledGeo) matchesAllow(countryUpper, cityLower string) bool {
	if len(g.allowCountries) > 0 && g.allowCountries[countryUpper] {
		return true
	}
	if len(g.allowCities) > 0 && g.allowCities[cityLower] {
		return true
	}
	return false
}

// matchesDeny checks if the result matches any deny rule.
func (g *CompiledGeo) matchesDeny(countryUpper, cityLower string) bool {
	if len(g.denyCountries) > 0 && g.denyCountries[countryUpper] {
		return true
	}
	if len(g.denyCities) > 0 && g.denyCities[cityLower] {
		return true
	}
	return false
}

// Status returns the admin API snapshot.
func (g *CompiledGeo) Status() GeoSnapshot {
	snap := GeoSnapshot{
		RouteID:       g.routeID,
		Enabled:       true,
		Order:         g.order,
		ShadowMode:    g.shadowMode,
		InjectHeaders: g.injectHeaders,
		Metrics: map[string]int64{
			"total_requests": g.metrics.TotalRequests.Load(),
			"allowed":        g.metrics.Allowed.Load(),
			"denied":         g.metrics.Denied.Load(),
			"lookup_errors":  g.metrics.LookupErrors.Load(),
		},
	}
	for c := range g.allowCountries {
		snap.AllowCountries = append(snap.AllowCountries, c)
	}
	for c := range g.denyCountries {
		snap.DenyCountries = append(snap.DenyCountries, c)
	}
	for c := range g.allowCities {
		snap.AllowCities = append(snap.AllowCities, c)
	}
	for c := range g.denyCities {
		snap.DenyCities = append(snap.DenyCities, c)
	}
	return snap
}

// MergeGeoConfig merges per-route config over global config.
func MergeGeoConfig(perRoute, global config.GeoConfig) config.GeoConfig {
	merged := config.MergeNonZero(global, perRoute)
	// InjectHeaders: only take per-route value if per-route has explicit settings,
	// otherwise inherit from global (can't distinguish "not set" from "set to false").
	if !(len(perRoute.AllowCountries) > 0 || len(perRoute.DenyCountries) > 0 ||
		len(perRoute.AllowCities) > 0 || len(perRoute.DenyCities) > 0 ||
		perRoute.Order != "") {
		merged.InjectHeaders = global.InjectHeaders
	}
	return merged
}
