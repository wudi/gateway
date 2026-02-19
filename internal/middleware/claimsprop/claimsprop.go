package claimsprop

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/variables"
)

// ClaimsPropagator propagates JWT claims as request headers.
type ClaimsPropagator struct {
	claims     map[string]string // claim_name -> header_name
	propagated atomic.Int64
}

// New creates a new ClaimsPropagator.
func New(cfg config.ClaimsPropagationConfig) *ClaimsPropagator {
	return &ClaimsPropagator{
		claims: cfg.Claims,
	}
}

// Apply reads claims from the request identity and sets mapped headers.
func (cp *ClaimsPropagator) Apply(r *http.Request) {
	varCtx := variables.GetFromRequest(r)
	if varCtx.Identity == nil || len(varCtx.Identity.Claims) == 0 {
		return
	}

	propagated := false
	for claimName, headerName := range cp.claims {
		val := extractClaim(varCtx.Identity.Claims, claimName)
		if val == "" {
			continue
		}
		r.Header.Set(headerName, val)
		propagated = true
	}
	if propagated {
		cp.propagated.Add(1)
	}
}

// extractClaim extracts a claim value, supporting dot notation for nested claims.
func extractClaim(claims map[string]interface{}, name string) string {
	parts := strings.Split(name, ".")

	var current interface{} = claims
	for _, part := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return ""
		}
		current, ok = m[part]
		if !ok {
			return ""
		}
	}

	if current == nil {
		return ""
	}

	switch v := current.(type) {
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Stats returns propagation statistics.
func (cp *ClaimsPropagator) Stats() map[string]interface{} {
	claims := make(map[string]string, len(cp.claims))
	for k, v := range cp.claims {
		claims[k] = v
	}
	return map[string]interface{}{
		"claims":     claims,
		"propagated": cp.propagated.Load(),
	}
}

// ClaimsPropByRoute manages per-route claims propagators.
type ClaimsPropByRoute struct {
	byroute.Manager[*ClaimsPropagator]
}

// NewClaimsPropByRoute creates a new claims propagation manager.
func NewClaimsPropByRoute() *ClaimsPropByRoute {
	return &ClaimsPropByRoute{}
}

// AddRoute adds a claims propagator for a route.
func (m *ClaimsPropByRoute) AddRoute(routeID string, cfg config.ClaimsPropagationConfig) {
	m.Add(routeID, New(cfg))
}

// GetPropagator returns the claims propagator for a route (may be nil).
func (m *ClaimsPropByRoute) GetPropagator(routeID string) *ClaimsPropagator {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route propagation statistics.
func (m *ClaimsPropByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(cp *ClaimsPropagator) interface{} { return cp.Stats() })
}
