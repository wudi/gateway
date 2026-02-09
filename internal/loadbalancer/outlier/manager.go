package outlier

import (
	"sync"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

// DetectorByRoute manages per-route outlier detectors.
type DetectorByRoute struct {
	mu        sync.RWMutex
	detectors map[string]*Detector
	onEject   func(routeID, backend, reason string)
	onRecover func(routeID, backend string)
}

// NewDetectorByRoute creates a new manager.
func NewDetectorByRoute() *DetectorByRoute {
	return &DetectorByRoute{
		detectors: make(map[string]*Detector),
	}
}

// SetCallbacks sets the ejection and recovery callbacks for all current and future detectors.
func (m *DetectorByRoute) SetCallbacks(onEject func(routeID, backend, reason string), onRecover func(routeID, backend string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEject = onEject
	m.onRecover = onRecover
	for _, d := range m.detectors {
		d.SetCallbacks(onEject, onRecover)
	}
}

// AddRoute creates and starts a detector for the given route.
func (m *DetectorByRoute) AddRoute(routeID string, cfg config.OutlierDetectionConfig, balancer loadbalancer.Balancer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	d := NewDetector(routeID, cfg, balancer)
	if m.onEject != nil || m.onRecover != nil {
		d.SetCallbacks(m.onEject, m.onRecover)
	}
	m.detectors[routeID] = d
}

// GetDetector returns the detector for a route, or nil.
func (m *DetectorByRoute) GetDetector(routeID string) *Detector {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.detectors[routeID]
}

// RouteIDs returns all route IDs with detectors.
func (m *DetectorByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.detectors))
	for id := range m.detectors {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns snapshots for all routes.
func (m *DetectorByRoute) Stats() map[string]DetectorSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]DetectorSnapshot, len(m.detectors))
	for id, d := range m.detectors {
		result[id] = d.Snapshot()
	}
	return result
}

// StopAll stops all detectors.
func (m *DetectorByRoute) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range m.detectors {
		d.Stop()
	}
	m.detectors = make(map[string]*Detector)
}
