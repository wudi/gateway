package abtest

import (
	"sync"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/canary"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

// ABTest collects per-traffic-group metrics for a running experiment.
type ABTest struct {
	routeID        string
	experimentName string
	startedAt      time.Time

	mu      sync.RWMutex
	metrics map[string]*canary.GroupMetrics
}

// New creates a new ABTest.
func New(routeID string, cfg config.ABTestConfig, wb *loadbalancer.WeightedBalancer) *ABTest {
	ab := &ABTest{
		routeID:        routeID,
		experimentName: cfg.ExperimentName,
		startedAt:      time.Now(),
		metrics:        make(map[string]*canary.GroupMetrics),
	}
	// Pre-populate metrics for each group in the weighted balancer.
	for _, g := range wb.GetGroups() {
		ab.metrics[g.Name] = canary.NewGroupMetrics()
	}
	return ab
}

// RecordRequest records a request outcome for a traffic group.
func (ab *ABTest) RecordRequest(group string, statusCode int, latency time.Duration) {
	ab.mu.RLock()
	gm, ok := ab.metrics[group]
	ab.mu.RUnlock()
	if !ok {
		return
	}
	gm.Record(statusCode, latency)
}

// Reset clears accumulated metrics and restarts the timer.
func (ab *ABTest) Reset() {
	ab.mu.Lock()
	defer ab.mu.Unlock()
	for _, gm := range ab.metrics {
		gm.Reset()
	}
	ab.startedAt = time.Now()
}

// ABTestSnapshot is a JSON-serializable view of an A/B test.
type ABTestSnapshot struct {
	RouteID        string                          `json:"route_id"`
	ExperimentName string                          `json:"experiment_name"`
	StartedAt      time.Time                       `json:"started_at"`
	DurationSec    float64                         `json:"duration_sec"`
	Groups         map[string]canary.GroupSnapshot  `json:"groups"`
}

// Snapshot returns a point-in-time view of the A/B test metrics.
func (ab *ABTest) Snapshot() ABTestSnapshot {
	ab.mu.RLock()
	defer ab.mu.RUnlock()
	groups := make(map[string]canary.GroupSnapshot, len(ab.metrics))
	for name, gm := range ab.metrics {
		groups[name] = gm.Snapshot()
	}
	return ABTestSnapshot{
		RouteID:        ab.routeID,
		ExperimentName: ab.experimentName,
		StartedAt:      ab.startedAt,
		DurationSec:    time.Since(ab.startedAt).Seconds(),
		Groups:         groups,
	}
}

// ABTestByRoute manages per-route A/B tests.
type ABTestByRoute struct {
	byroute.Manager[*ABTest]
}

// NewABTestByRoute creates a new ABTestByRoute.
func NewABTestByRoute() *ABTestByRoute {
	return &ABTestByRoute{}
}

// AddRoute creates and stores an A/B test for a route.
func (m *ABTestByRoute) AddRoute(routeID string, cfg config.ABTestConfig, wb *loadbalancer.WeightedBalancer) {
	m.Add(routeID, New(routeID, cfg, wb))
}

// GetTest returns the A/B test for a route, or nil.
func (m *ABTestByRoute) GetTest(routeID string) *ABTest {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all routes.
func (m *ABTestByRoute) Stats() map[string]ABTestSnapshot {
	return byroute.CollectStats(&m.Manager, func(ab *ABTest) ABTestSnapshot { return ab.Snapshot() })
}
