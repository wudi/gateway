package trafficshape

import (
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
)

// ThrottleByRoute manages per-route throttlers.
type ThrottleByRoute struct {
	byroute.Manager[*Throttler]
}

// NewThrottleByRoute creates a new ThrottleByRoute.
func NewThrottleByRoute() *ThrottleByRoute {
	return &ThrottleByRoute{}
}

// AddRoute creates and stores a throttler for a route.
func (m *ThrottleByRoute) AddRoute(routeID string, cfg config.ThrottleConfig) {
	m.Add(routeID, NewThrottler(cfg.Rate, cfg.Burst, cfg.MaxWait, cfg.PerIP))
}

// GetThrottler returns the throttler for a route, or nil.
func (m *ThrottleByRoute) GetThrottler(routeID string) *Throttler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *ThrottleByRoute) Stats() map[string]ThrottleSnapshot {
	result := make(map[string]ThrottleSnapshot)
	m.Range(func(id string, t *Throttler) bool {
		result[id] = t.Snapshot()
		return true
	})
	return result
}

// BandwidthByRoute manages per-route bandwidth limiters.
type BandwidthByRoute struct {
	byroute.Manager[*BandwidthLimiter]
}

// NewBandwidthByRoute creates a new BandwidthByRoute.
func NewBandwidthByRoute() *BandwidthByRoute {
	return &BandwidthByRoute{}
}

// AddRoute creates and stores a bandwidth limiter for a route.
func (m *BandwidthByRoute) AddRoute(routeID string, cfg config.BandwidthConfig) {
	m.Add(routeID, NewBandwidthLimiter(cfg.RequestRate, cfg.ResponseRate, cfg.RequestBurst, cfg.ResponseBurst))
}

// GetLimiter returns the bandwidth limiter for a route, or nil.
func (m *BandwidthByRoute) GetLimiter(routeID string) *BandwidthLimiter {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *BandwidthByRoute) Stats() map[string]BandwidthSnapshot {
	result := make(map[string]BandwidthSnapshot)
	m.Range(func(id string, l *BandwidthLimiter) bool {
		result[id] = l.Snapshot()
		return true
	})
	return result
}

// PriorityByRoute stores per-route priority configs for level determination.
type PriorityByRoute struct {
	byroute.Manager[config.PriorityConfig]
}

// NewPriorityByRoute creates a new PriorityByRoute.
func NewPriorityByRoute() *PriorityByRoute {
	return &PriorityByRoute{}
}

// AddRoute stores a priority config for a route.
func (m *PriorityByRoute) AddRoute(routeID string, cfg config.PriorityConfig) {
	m.Add(routeID, cfg)
}

// GetConfig returns the priority config for a route, or a zero value.
func (m *PriorityByRoute) GetConfig(routeID string) (config.PriorityConfig, bool) {
	return m.Get(routeID)
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
	byroute.Manager[*FaultInjector]
}

// NewFaultInjectionByRoute creates a new FaultInjectionByRoute.
func NewFaultInjectionByRoute() *FaultInjectionByRoute {
	return &FaultInjectionByRoute{}
}

// AddRoute creates and stores a fault injector for a route.
func (m *FaultInjectionByRoute) AddRoute(routeID string, cfg config.FaultInjectionConfig) {
	m.Add(routeID, NewFaultInjector(cfg))
}

// GetInjector returns the fault injector for a route, or nil.
func (m *FaultInjectionByRoute) GetInjector(routeID string) *FaultInjector {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *FaultInjectionByRoute) Stats() map[string]FaultInjectionSnapshot {
	result := make(map[string]FaultInjectionSnapshot)
	m.Range(func(id string, fi *FaultInjector) bool {
		result[id] = fi.Snapshot()
		return true
	})
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
