package trafficshape

import (
	"sync"
	"time"

	"github.com/example/gateway/internal/config"
)

// ThrottleByRoute manages per-route throttlers.
type ThrottleByRoute struct {
	throttlers map[string]*Throttler
	mu         sync.RWMutex
}

// NewThrottleByRoute creates a new ThrottleByRoute.
func NewThrottleByRoute() *ThrottleByRoute {
	return &ThrottleByRoute{
		throttlers: make(map[string]*Throttler),
	}
}

// AddRoute creates and stores a throttler for a route.
func (m *ThrottleByRoute) AddRoute(routeID string, cfg config.ThrottleConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.throttlers[routeID] = NewThrottler(cfg.Rate, cfg.Burst, cfg.MaxWait, cfg.PerIP)
}

// GetThrottler returns the throttler for a route, or nil.
func (m *ThrottleByRoute) GetThrottler(routeID string) *Throttler {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.throttlers[routeID]
}

// RouteIDs returns all route IDs with throttlers.
func (m *ThrottleByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.throttlers))
	for id := range m.throttlers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns snapshots for all routes.
func (m *ThrottleByRoute) Stats() map[string]ThrottleSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]ThrottleSnapshot, len(m.throttlers))
	for id, t := range m.throttlers {
		result[id] = t.Snapshot()
	}
	return result
}

// BandwidthByRoute manages per-route bandwidth limiters.
type BandwidthByRoute struct {
	limiters map[string]*BandwidthLimiter
	mu       sync.RWMutex
}

// NewBandwidthByRoute creates a new BandwidthByRoute.
func NewBandwidthByRoute() *BandwidthByRoute {
	return &BandwidthByRoute{
		limiters: make(map[string]*BandwidthLimiter),
	}
}

// AddRoute creates and stores a bandwidth limiter for a route.
func (m *BandwidthByRoute) AddRoute(routeID string, cfg config.BandwidthConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limiters[routeID] = NewBandwidthLimiter(cfg.RequestRate, cfg.ResponseRate, cfg.RequestBurst, cfg.ResponseBurst)
}

// GetLimiter returns the bandwidth limiter for a route, or nil.
func (m *BandwidthByRoute) GetLimiter(routeID string) *BandwidthLimiter {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limiters[routeID]
}

// RouteIDs returns all route IDs with bandwidth limiters.
func (m *BandwidthByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.limiters))
	for id := range m.limiters {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns snapshots for all routes.
func (m *BandwidthByRoute) Stats() map[string]BandwidthSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]BandwidthSnapshot, len(m.limiters))
	for id, l := range m.limiters {
		result[id] = l.Snapshot()
	}
	return result
}

// PriorityByRoute stores per-route priority configs for level determination.
type PriorityByRoute struct {
	configs map[string]config.PriorityConfig
	mu      sync.RWMutex
}

// NewPriorityByRoute creates a new PriorityByRoute.
func NewPriorityByRoute() *PriorityByRoute {
	return &PriorityByRoute{
		configs: make(map[string]config.PriorityConfig),
	}
}

// AddRoute stores a priority config for a route.
func (m *PriorityByRoute) AddRoute(routeID string, cfg config.PriorityConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configs[routeID] = cfg
}

// GetConfig returns the priority config for a route, or a zero value.
func (m *PriorityByRoute) GetConfig(routeID string) (config.PriorityConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, ok := m.configs[routeID]
	return cfg, ok
}

// RouteIDs returns all route IDs with priority configs.
func (m *PriorityByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.configs))
	for id := range m.configs {
		ids = append(ids, id)
	}
	return ids
}

// MergeThrottleConfig merges a route-level throttle config with the global config as fallback.
func MergeThrottleConfig(route, global config.ThrottleConfig) config.ThrottleConfig {
	if route.Rate == 0 {
		route.Rate = global.Rate
	}
	if route.Burst == 0 {
		route.Burst = global.Burst
	}
	if route.MaxWait == 0 {
		route.MaxWait = global.MaxWait
	}
	if route.MaxWait == 0 {
		route.MaxWait = 30 * time.Second
	}
	return route
}

// MergeBandwidthConfig merges a route-level bandwidth config with the global config as fallback.
func MergeBandwidthConfig(route, global config.BandwidthConfig) config.BandwidthConfig {
	if route.RequestRate == 0 {
		route.RequestRate = global.RequestRate
	}
	if route.ResponseRate == 0 {
		route.ResponseRate = global.ResponseRate
	}
	if route.RequestBurst == 0 {
		route.RequestBurst = global.RequestBurst
	}
	if route.ResponseBurst == 0 {
		route.ResponseBurst = global.ResponseBurst
	}
	return route
}

// MergePriorityConfig merges a route-level priority config with the global config as fallback.
func MergePriorityConfig(route, global config.PriorityConfig) config.PriorityConfig {
	if route.DefaultLevel == 0 {
		route.DefaultLevel = global.DefaultLevel
	}
	if len(route.Levels) == 0 {
		route.Levels = global.Levels
	}
	if route.MaxWait == 0 {
		route.MaxWait = global.MaxWait
	}
	return route
}

// FaultInjectionByRoute manages per-route fault injectors.
type FaultInjectionByRoute struct {
	injectors map[string]*FaultInjector
	mu        sync.RWMutex
}

// NewFaultInjectionByRoute creates a new FaultInjectionByRoute.
func NewFaultInjectionByRoute() *FaultInjectionByRoute {
	return &FaultInjectionByRoute{
		injectors: make(map[string]*FaultInjector),
	}
}

// AddRoute creates and stores a fault injector for a route.
func (m *FaultInjectionByRoute) AddRoute(routeID string, cfg config.FaultInjectionConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.injectors[routeID] = NewFaultInjector(cfg)
}

// GetInjector returns the fault injector for a route, or nil.
func (m *FaultInjectionByRoute) GetInjector(routeID string) *FaultInjector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.injectors[routeID]
}

// RouteIDs returns all route IDs with fault injectors.
func (m *FaultInjectionByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.injectors))
	for id := range m.injectors {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns snapshots for all routes.
func (m *FaultInjectionByRoute) Stats() map[string]FaultInjectionSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]FaultInjectionSnapshot, len(m.injectors))
	for id, fi := range m.injectors {
		result[id] = fi.Snapshot()
	}
	return result
}

// MergeFaultInjectionConfig merges a route-level fault injection config with the global config as fallback.
func MergeFaultInjectionConfig(route, global config.FaultInjectionConfig) config.FaultInjectionConfig {
	if route.Delay.Percentage == 0 && global.Delay.Percentage > 0 {
		route.Delay = global.Delay
	}
	if route.Abort.Percentage == 0 && global.Abort.Percentage > 0 {
		route.Abort = global.Abort
	}
	return route
}
