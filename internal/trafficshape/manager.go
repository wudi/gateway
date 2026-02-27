package trafficshape

import (
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
)

// ThrottleByRoute manages per-route throttlers.
type ThrottleByRoute = byroute.Factory[*Throttler, config.ThrottleConfig]

// NewThrottleByRoute creates a new ThrottleByRoute.
func NewThrottleByRoute() *ThrottleByRoute {
	return byroute.SimpleFactory(func(cfg config.ThrottleConfig) *Throttler {
		return NewThrottler(cfg.Rate, cfg.Burst, cfg.MaxWait, cfg.PerIP)
	}, func(t *Throttler) any { return t.Snapshot() })
}

// BandwidthByRoute manages per-route bandwidth limiters.
type BandwidthByRoute = byroute.Factory[*BandwidthLimiter, config.BandwidthConfig]

// NewBandwidthByRoute creates a new BandwidthByRoute.
func NewBandwidthByRoute() *BandwidthByRoute {
	return byroute.SimpleFactory(func(cfg config.BandwidthConfig) *BandwidthLimiter {
		return NewBandwidthLimiter(cfg.RequestRate, cfg.ResponseRate, cfg.RequestBurst, cfg.ResponseBurst)
	}, func(l *BandwidthLimiter) any { return l.Snapshot() })
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
	merged := config.MergeNonZero(global, route)
	if merged.MaxWait == 0 {
		merged.MaxWait = 30 * time.Second
	}
	return merged
}

// MergeBandwidthConfig merges a route-level bandwidth config with the global config as fallback.
func MergeBandwidthConfig(route, global config.BandwidthConfig) config.BandwidthConfig {
	return config.MergeNonZero(global, route)
}

// MergePriorityConfig merges a route-level priority config with the global config as fallback.
func MergePriorityConfig(route, global config.PriorityConfig) config.PriorityConfig {
	return config.MergeNonZero(global, route)
}

// FaultInjectionByRoute manages per-route fault injectors.
type FaultInjectionByRoute = byroute.Factory[*FaultInjector, config.FaultInjectionConfig]

// NewFaultInjectionByRoute creates a new FaultInjectionByRoute.
func NewFaultInjectionByRoute() *FaultInjectionByRoute {
	return byroute.SimpleFactory(NewFaultInjector, func(fi *FaultInjector) any { return fi.Snapshot() })
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
