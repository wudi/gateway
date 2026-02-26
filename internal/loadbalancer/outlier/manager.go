package outlier

import (
	"sync"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
)

// DetectorByRoute manages per-route outlier detectors.
type DetectorByRoute struct {
	byroute.Manager[*Detector]
	mu        sync.Mutex
	onEject   func(routeID, backend, reason string)
	onRecover func(routeID, backend string)
}

// NewDetectorByRoute creates a new manager.
func NewDetectorByRoute() *DetectorByRoute {
	return &DetectorByRoute{}
}

// SetCallbacks sets the ejection and recovery callbacks for all current and future detectors.
func (m *DetectorByRoute) SetCallbacks(onEject func(routeID, backend, reason string), onRecover func(routeID, backend string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEject = onEject
	m.onRecover = onRecover
	m.Range(func(_ string, d *Detector) bool {
		d.SetCallbacks(onEject, onRecover)
		return true
	})
}

// AddRoute creates and starts a detector for the given route.
func (m *DetectorByRoute) AddRoute(routeID string, cfg config.OutlierDetectionConfig, balancer loadbalancer.Balancer) {
	m.mu.Lock()
	onEject := m.onEject
	onRecover := m.onRecover
	m.mu.Unlock()

	d := NewDetector(routeID, cfg, balancer)
	if onEject != nil || onRecover != nil {
		d.SetCallbacks(onEject, onRecover)
	}
	m.Add(routeID, d)
}

// GetDetector returns the detector for a route, or nil.
func (m *DetectorByRoute) GetDetector(routeID string) *Detector {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *DetectorByRoute) Stats() map[string]DetectorSnapshot {
	return byroute.CollectStats(&m.Manager, func(d *Detector) DetectorSnapshot { return d.Snapshot() })
}

// StopAll stops all detectors and removes them.
func (m *DetectorByRoute) StopAll() {
	m.Range(func(_ string, d *Detector) bool {
		d.Stop()
		return true
	})
	m.Clear()
}
