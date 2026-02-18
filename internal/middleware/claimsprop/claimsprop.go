package claimsprop

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

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
	mu          sync.RWMutex
	propagators map[string]*ClaimsPropagator
}

// NewClaimsPropByRoute creates a new claims propagation manager.
func NewClaimsPropByRoute() *ClaimsPropByRoute {
	return &ClaimsPropByRoute{}
}

// AddRoute adds a claims propagator for a route.
func (m *ClaimsPropByRoute) AddRoute(routeID string, cfg config.ClaimsPropagationConfig) {
	cp := New(cfg)
	m.mu.Lock()
	if m.propagators == nil {
		m.propagators = make(map[string]*ClaimsPropagator)
	}
	m.propagators[routeID] = cp
	m.mu.Unlock()
}

// GetPropagator returns the claims propagator for a route (may be nil).
func (m *ClaimsPropByRoute) GetPropagator(routeID string) *ClaimsPropagator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.propagators[routeID]
}

// RouteIDs returns all route IDs with claims propagation.
func (m *ClaimsPropByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.propagators))
	for id := range m.propagators {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route propagation statistics.
func (m *ClaimsPropByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]interface{}, len(m.propagators))
	for id, cp := range m.propagators {
		result[id] = cp.Stats()
	}
	return result
}
