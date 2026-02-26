package edgecacherules

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
)

// compiledRule is a pre-compiled version of an EdgeCacheRule.
type compiledRule struct {
	statusCodes  map[int]struct{}
	contentTypes []string
	pathPatterns []string
	cacheControl string
	vary         string
	override     bool
}

// EdgeCacheRules evaluates conditional rules to set cache-control headers.
type EdgeCacheRules struct {
	rules   []compiledRule
	applied atomic.Int64
}

// Snapshot is a point-in-time copy of edge cache rules metrics.
type Snapshot struct {
	Applied   int64 `json:"applied"`
	RuleCount int   `json:"rule_count"`
}

// New creates EdgeCacheRules from config with pre-compiled patterns.
func New(cfg config.EdgeCacheRulesConfig) *EdgeCacheRules {
	rules := make([]compiledRule, 0, len(cfg.Rules))
	for _, r := range cfg.Rules {
		cr := compiledRule{
			pathPatterns: r.Match.PathPatterns,
			contentTypes: r.Match.ContentTypes,
			override:     true, // default
		}
		if r.Override != nil {
			cr.override = *r.Override
		}

		// Build status code lookup
		if len(r.Match.StatusCodes) > 0 {
			cr.statusCodes = make(map[int]struct{}, len(r.Match.StatusCodes))
			for _, sc := range r.Match.StatusCodes {
				cr.statusCodes[sc] = struct{}{}
			}
		}

		// Build Cache-Control value
		cr.cacheControl = buildCacheControl(r)

		if len(r.Vary) > 0 {
			cr.vary = strings.Join(r.Vary, ", ")
		}

		rules = append(rules, cr)
	}
	return &EdgeCacheRules{rules: rules}
}

// buildCacheControl constructs a Cache-Control header value from a rule.
func buildCacheControl(r config.EdgeCacheRule) string {
	if r.CacheControl != "" {
		return r.CacheControl
	}
	if r.NoStore {
		return "no-store"
	}

	var parts []string
	if r.Private {
		parts = append(parts, "private")
	} else {
		parts = append(parts, "public")
	}
	if r.MaxAge > 0 {
		parts = append(parts, fmt.Sprintf("max-age=%d", r.MaxAge))
	}
	if r.SMaxAge > 0 {
		parts = append(parts, fmt.Sprintf("s-maxage=%d", r.SMaxAge))
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// match checks if a compiled rule matches the given response properties.
func (cr *compiledRule) match(statusCode int, contentType, reqPath string) bool {
	// Status code check
	if len(cr.statusCodes) > 0 {
		if _, ok := cr.statusCodes[statusCode]; !ok {
			return false
		}
	}

	// Content-Type check (prefix match for charset variants)
	if len(cr.contentTypes) > 0 {
		matched := false
		for _, ct := range cr.contentTypes {
			if strings.HasPrefix(contentType, ct) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Path pattern check (glob)
	if len(cr.pathPatterns) > 0 {
		matched := false
		for _, pattern := range cr.pathPatterns {
			if ok, _ := path.Match(pattern, reqPath); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// Evaluate checks rules against response properties and returns the first matching rule's headers.
// Returns (cacheControl, vary, override, matched).
func (e *EdgeCacheRules) Evaluate(statusCode int, contentType, reqPath string) (cacheControl string, vary string, override bool, matched bool) {
	for i := range e.rules {
		r := &e.rules[i]
		if r.match(statusCode, contentType, reqPath) {
			e.applied.Add(1)
			return r.cacheControl, r.vary, r.override, true
		}
	}
	return "", "", false, false
}

// Stats returns a snapshot of edge cache rules metrics.
func (e *EdgeCacheRules) Stats() Snapshot {
	return Snapshot{
		Applied:   e.applied.Load(),
		RuleCount: len(e.rules),
	}
}

// Middleware returns a middleware that evaluates edge cache rules on responses.
func (e *EdgeCacheRules) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ew := &edgeCacheWriter{
				ResponseWriter: w,
				rules:          e,
				reqPath:        r.URL.Path,
			}
			next.ServeHTTP(ew, r)
		})
	}
}

type edgeCacheWriter struct {
	http.ResponseWriter
	rules       *EdgeCacheRules
	reqPath     string
	wroteHeader bool
}

func (w *edgeCacheWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.applyRules(code)
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *edgeCacheWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		w.applyRules(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *edgeCacheWriter) applyRules(statusCode int) {
	ct := w.ResponseWriter.Header().Get("Content-Type")
	cc, vary, override, matched := w.rules.Evaluate(statusCode, ct, w.reqPath)
	if !matched {
		return
	}
	if cc != "" {
		if override || w.ResponseWriter.Header().Get("Cache-Control") == "" {
			w.ResponseWriter.Header().Set("Cache-Control", cc)
		}
	}
	if vary != "" {
		w.ResponseWriter.Header().Set("Vary", vary)
	}
}

func (w *edgeCacheWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// MergeEdgeCacheRulesConfig merges per-route and global edge cache rules configs.
// Per-route takes precedence when enabled.
func MergeEdgeCacheRulesConfig(perRoute, global config.EdgeCacheRulesConfig) config.EdgeCacheRulesConfig {
	if perRoute.Enabled {
		return perRoute
	}
	return global
}

// EdgeCacheRulesByRoute manages edge cache rules per route.
type EdgeCacheRulesByRoute struct {
	byroute.Manager[*EdgeCacheRules]
}

// NewEdgeCacheRulesByRoute creates a new edge cache rules manager.
func NewEdgeCacheRulesByRoute() *EdgeCacheRulesByRoute {
	return &EdgeCacheRulesByRoute{}
}

// AddRoute adds edge cache rules for a route.
func (br *EdgeCacheRulesByRoute) AddRoute(routeID string, cfg config.EdgeCacheRulesConfig) {
	br.Add(routeID, New(cfg))
}

// GetHandler returns the edge cache rules for a route.
func (br *EdgeCacheRulesByRoute) GetHandler(routeID string) *EdgeCacheRules {
	v, _ := br.Get(routeID)
	return v
}

// Stats returns edge cache rules statistics for all routes.
func (br *EdgeCacheRulesByRoute) Stats() map[string]Snapshot {
	return byroute.CollectStats(&br.Manager, func(e *EdgeCacheRules) Snapshot { return e.Stats() })
}
