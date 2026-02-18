package paramforward

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
)

// essentialHeaders are always preserved regardless of whitelist.
var essentialHeaders = map[string]bool{
	"host":              true,
	"content-type":      true,
	"content-length":    true,
	"transfer-encoding": true,
	"connection":        true,
	"accept-encoding":   true,
	"user-agent":        true,
}

// ParamForwarder strips request headers, query params, and cookies
// that are not in the configured whitelist.
type ParamForwarder struct {
	allowedHeaders map[string]bool
	allowedQuery   map[string]bool
	allowedCookies map[string]bool
	stripped       atomic.Int64
}

// New creates a ParamForwarder from config.
func New(cfg config.ParamForwardingConfig) *ParamForwarder {
	pf := &ParamForwarder{}

	if len(cfg.Headers) > 0 {
		pf.allowedHeaders = make(map[string]bool, len(cfg.Headers))
		for _, h := range cfg.Headers {
			pf.allowedHeaders[strings.ToLower(h)] = true
		}
	}

	if len(cfg.QueryParams) > 0 {
		pf.allowedQuery = make(map[string]bool, len(cfg.QueryParams))
		for _, q := range cfg.QueryParams {
			pf.allowedQuery[q] = true
		}
	}

	if len(cfg.Cookies) > 0 {
		pf.allowedCookies = make(map[string]bool, len(cfg.Cookies))
		for _, c := range cfg.Cookies {
			pf.allowedCookies[c] = true
		}
	}

	return pf
}

// Middleware returns a middleware that strips disallowed params.
func (pf *ParamForwarder) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var count int64

			// Filter headers
			if pf.allowedHeaders != nil {
				var toDelete []string
				for name := range r.Header {
					lower := strings.ToLower(name)
					if essentialHeaders[lower] {
						continue
					}
					if !pf.allowedHeaders[lower] {
						toDelete = append(toDelete, name)
					}
				}
				for _, name := range toDelete {
					r.Header.Del(name)
					count++
				}
			}

			// Filter query params
			if pf.allowedQuery != nil {
				q := r.URL.Query()
				filtered := make(url.Values)
				for key, vals := range q {
					if pf.allowedQuery[key] {
						filtered[key] = vals
					} else {
						count++
					}
				}
				r.URL.RawQuery = filtered.Encode()
			}

			// Filter cookies
			if pf.allowedCookies != nil {
				cookies := r.Cookies()
				var kept []*http.Cookie
				for _, c := range cookies {
					if pf.allowedCookies[c.Name] {
						kept = append(kept, c)
					} else {
						count++
					}
				}
				// Rebuild Cookie header
				r.Header.Del("Cookie")
				for _, c := range kept {
					r.AddCookie(c)
				}
			}

			if count > 0 {
				pf.stripped.Add(count)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Stripped returns the number of parameters stripped.
func (pf *ParamForwarder) Stripped() int64 {
	return pf.stripped.Load()
}

// ParamForwardByRoute manages per-route param forwarders.
type ParamForwardByRoute struct {
	forwarders map[string]*ParamForwarder
	mu         sync.RWMutex
}

// NewParamForwardByRoute creates a new per-route param forwarder manager.
func NewParamForwardByRoute() *ParamForwardByRoute {
	return &ParamForwardByRoute{}
}

// AddRoute adds a param forwarder for a route.
func (m *ParamForwardByRoute) AddRoute(routeID string, cfg config.ParamForwardingConfig) {
	pf := New(cfg)
	m.mu.Lock()
	if m.forwarders == nil {
		m.forwarders = make(map[string]*ParamForwarder)
	}
	m.forwarders[routeID] = pf
	m.mu.Unlock()
}

// GetForwarder returns the param forwarder for a route.
func (m *ParamForwardByRoute) GetForwarder(routeID string) *ParamForwarder {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.forwarders[routeID]
}

// RouteIDs returns all route IDs with param forwarders.
func (m *ParamForwardByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.forwarders))
	for id := range m.forwarders {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route param forwarder stats.
func (m *ParamForwardByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[string]interface{}, len(m.forwarders))
	for id, pf := range m.forwarders {
		stats[id] = map[string]interface{}{
			"stripped":        pf.Stripped(),
			"allowed_headers": len(pf.allowedHeaders),
			"allowed_query":   len(pf.allowedQuery),
			"allowed_cookies": len(pf.allowedCookies),
		}
	}
	return stats
}
