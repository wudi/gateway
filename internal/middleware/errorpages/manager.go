package errorpages

import (
	"fmt"
	"sort"
	"sync"

	"github.com/example/gateway/internal/config"
)

// ErrorPagesByRoute manages per-route compiled error pages.
type ErrorPagesByRoute struct {
	mu    sync.RWMutex
	pages map[string]*CompiledErrorPages
}

// NewErrorPagesByRoute creates a new error pages manager.
func NewErrorPagesByRoute() *ErrorPagesByRoute {
	return &ErrorPagesByRoute{
		pages: make(map[string]*CompiledErrorPages),
	}
}

// AddRoute registers compiled error pages for the given route.
// It takes both global and per-route configs for merge at compile time.
func (m *ErrorPagesByRoute) AddRoute(routeID string, globalCfg, routeCfg config.ErrorPagesConfig) error {
	ep, err := New(globalCfg, routeCfg)
	if err != nil {
		return err
	}
	if ep == nil {
		return nil
	}
	m.mu.Lock()
	m.pages[routeID] = ep
	m.mu.Unlock()
	return nil
}

// GetErrorPages returns the compiled error pages for a route, or nil if none configured.
func (m *ErrorPagesByRoute) GetErrorPages(routeID string) *CompiledErrorPages {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pages[routeID]
}

// RouteIDs returns all route IDs that have error pages configured.
func (m *ErrorPagesByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.pages))
	for id := range m.pages {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns error page status for all routes.
func (m *ErrorPagesByRoute) Stats() map[string]ErrorPagesStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]ErrorPagesStatus, len(m.pages))
	for id, ep := range m.pages {
		keys := make([]string, 0)
		for code := range ep.exactPages {
			keys = append(keys, fmt.Sprintf("%d", code))
		}
		for base := range ep.classPages {
			keys = append(keys, fmt.Sprintf("%dxx", base/100))
		}
		if ep.defaultPage != nil {
			keys = append(keys, "default")
		}
		sort.Strings(keys)
		result[id] = ErrorPagesStatus{
			PageKeys: keys,
			Metrics:  ep.Metrics(),
		}
	}
	return result
}
