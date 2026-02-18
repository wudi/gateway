package accesslog

import (
	"sync"

	"github.com/wudi/gateway/internal/config"
)

// AccessLogStatus represents the status of an access log config for admin API.
type AccessLogStatus struct {
	Enabled        *bool    `json:"enabled,omitempty"`
	Format         string   `json:"format,omitempty"`
	BodyCapture    bool     `json:"body_capture"`
	StatusCodes    []string `json:"status_codes,omitempty"`
	Methods        []string `json:"methods,omitempty"`
	SampleRate     float64  `json:"sample_rate,omitempty"`
	HeadersInclude []string `json:"headers_include,omitempty"`
}

// AccessLogByRoute manages per-route access log configs.
type AccessLogByRoute struct {
	mu      sync.RWMutex
	configs map[string]*CompiledAccessLog
	raw     map[string]config.AccessLogConfig
}

// NewAccessLogByRoute creates a new AccessLogByRoute.
func NewAccessLogByRoute() *AccessLogByRoute {
	return &AccessLogByRoute{}
}

// AddRoute compiles and stores an access log config for the given route.
func (m *AccessLogByRoute) AddRoute(routeID string, cfg config.AccessLogConfig) error {
	compiled, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.configs == nil {
		m.configs = make(map[string]*CompiledAccessLog)
		m.raw = make(map[string]config.AccessLogConfig)
	}
	m.configs[routeID] = compiled
	m.raw[routeID] = cfg
	m.mu.Unlock()
	return nil
}

// GetConfig returns the compiled access log config for a route, or nil.
func (m *AccessLogByRoute) GetConfig(routeID string) *CompiledAccessLog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.configs[routeID]
}

// RouteIDs returns all route IDs with access log configs.
func (m *AccessLogByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.configs))
	for id := range m.configs {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns admin-facing status for all routes.
func (m *AccessLogByRoute) Stats() map[string]AccessLogStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]AccessLogStatus, len(m.raw))
	for id, cfg := range m.raw {
		result[id] = AccessLogStatus{
			Enabled:        cfg.Enabled,
			Format:         cfg.Format,
			BodyCapture:    cfg.Body.Enabled,
			StatusCodes:    cfg.Conditions.StatusCodes,
			Methods:        cfg.Conditions.Methods,
			SampleRate:     cfg.Conditions.SampleRate,
			HeadersInclude: cfg.HeadersInclude,
		}
	}
	return result
}
