package errorpages

import (
	"fmt"
	"sort"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// ErrorPagesByRoute manages per-route compiled error pages.
type ErrorPagesByRoute struct {
	byroute.Manager[*CompiledErrorPages]
}

// NewErrorPagesByRoute creates a new error pages manager.
func NewErrorPagesByRoute() *ErrorPagesByRoute {
	return &ErrorPagesByRoute{}
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
	m.Add(routeID, ep)
	return nil
}

// GetErrorPages returns the compiled error pages for a route, or nil if none configured.
func (m *ErrorPagesByRoute) GetErrorPages(routeID string) *CompiledErrorPages {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns error page status for all routes.
func (m *ErrorPagesByRoute) Stats() map[string]ErrorPagesStatus {
	return byroute.CollectStats(&m.Manager, func(ep *CompiledErrorPages) ErrorPagesStatus {
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
		return ErrorPagesStatus{
			PageKeys: keys,
			Metrics:  ep.Metrics(),
		}
	})
}
