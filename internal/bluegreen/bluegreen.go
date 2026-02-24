package bluegreen

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/health"
	"github.com/wudi/gateway/internal/loadbalancer"
)

// State represents the blue-green deployment state machine.
type State string

const (
	StateInactive       State = "inactive"
	StateHealthChecking State = "health_checking"
	StatePromoting      State = "promoting"
	StateActive         State = "active"
	StateRolledBack     State = "rolled_back"
)

// Controller manages blue-green deployments for a single route.
type Controller struct {
	mu              sync.RWMutex
	routeID         string
	state           State
	activeGroup     string
	inactiveGroup   string
	balancer        *loadbalancer.WeightedBalancer
	healthChecker   *health.Checker
	originalWeights map[string]int
	cfg             config.BlueGreenConfig

	// Observation metrics
	metrics     GroupMetrics
	promoteTime time.Time
	stopCh      chan struct{}
	stopped     atomic.Bool
}

// GroupMetrics tracks per-group request metrics for observation.
type GroupMetrics struct {
	mu       sync.Mutex
	requests map[string]*groupStats
}

type groupStats struct {
	total  atomic.Int64
	errors atomic.Int64
	totalLatencyNs atomic.Int64
}

func newGroupMetrics() GroupMetrics {
	return GroupMetrics{requests: make(map[string]*groupStats)}
}

func (m *GroupMetrics) getOrCreate(group string) *groupStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.requests[group]; ok {
		return s
	}
	s := &groupStats{}
	m.requests[group] = s
	return s
}

// Snapshot is a point-in-time snapshot of blue-green state.
type Snapshot struct {
	RouteID         string            `json:"route_id"`
	State           State             `json:"state"`
	ActiveGroup     string            `json:"active_group"`
	InactiveGroup   string            `json:"inactive_group"`
	ErrorThreshold  float64           `json:"error_threshold"`
	RollbackOnError bool              `json:"rollback_on_error"`
	Groups          map[string]Stats  `json:"groups,omitempty"`
}

// Stats holds per-group request statistics.
type Stats struct {
	Requests     int64   `json:"requests"`
	Errors       int64   `json:"errors"`
	ErrorRate    float64 `json:"error_rate"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// NewController creates a new blue-green controller.
func NewController(routeID string, cfg config.BlueGreenConfig, balancer *loadbalancer.WeightedBalancer, hc *health.Checker) *Controller {
	// Save original weights
	origWeights := balancer.GetGroupWeights()

	ctrl := &Controller{
		routeID:         routeID,
		state:           StateInactive,
		activeGroup:     cfg.ActiveGroup,
		inactiveGroup:   cfg.InactiveGroup,
		balancer:        balancer,
		healthChecker:   hc,
		originalWeights: origWeights,
		cfg:             cfg,
		metrics:         newGroupMetrics(),
		stopCh:          make(chan struct{}),
	}

	return ctrl
}

// RecordRequest records a request for observation metrics.
func (c *Controller) RecordRequest(group string, statusCode int, latency time.Duration) {
	s := c.metrics.getOrCreate(group)
	s.total.Add(1)
	s.totalLatencyNs.Add(int64(latency))
	if statusCode >= 500 {
		s.errors.Add(1)
	}
}

// Promote initiates the promotion of the inactive group to active.
func (c *Controller) Promote() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state != StateInactive && c.state != StateRolledBack {
		return fmt.Errorf("cannot promote from state %s (must be inactive or rolled_back)", c.state)
	}

	// Switch weights: inactive gets 100%, active gets 0%
	weights := map[string]int{
		c.inactiveGroup: 100,
		c.activeGroup:   0,
	}
	c.balancer.SetGroupWeights(weights)

	c.state = StatePromoting
	c.promoteTime = time.Now()
	c.metrics = newGroupMetrics()

	// Start observation goroutine if rollback-on-error is enabled
	if c.cfg.RollbackOnError && c.cfg.ObservationWindow > 0 {
		go c.observe()
	}

	return nil
}

// Rollback reverts to the original traffic weights.
func (c *Controller) Rollback() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.state != StatePromoting && c.state != StateActive {
		return fmt.Errorf("cannot rollback from state %s", c.state)
	}

	c.balancer.SetGroupWeights(c.originalWeights)
	c.state = StateRolledBack

	return nil
}

func (c *Controller) observe() {
	window := c.cfg.ObservationWindow
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(window)
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.RLock()
			state := c.state
			c.mu.RUnlock()

			if state != StatePromoting {
				return
			}

			if time.Now().After(deadline) {
				// Observation window passed, mark as active
				c.mu.Lock()
				if c.state == StatePromoting {
					c.state = StateActive
				}
				c.mu.Unlock()
				return
			}

			// Check error rate on the promoted group
			s := c.metrics.getOrCreate(c.inactiveGroup)
			total := s.total.Load()
			if total >= int64(c.cfg.MinRequests) && c.cfg.MinRequests > 0 {
				errors := s.errors.Load()
				errorRate := float64(errors) / float64(total)
				if errorRate > c.cfg.ErrorThreshold {
					c.Rollback()
					return
				}
			}
		}
	}
}

// Snapshot returns a point-in-time snapshot of the controller state.
func (c *Controller) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := Snapshot{
		RouteID:         c.routeID,
		State:           c.state,
		ActiveGroup:     c.activeGroup,
		InactiveGroup:   c.inactiveGroup,
		ErrorThreshold:  c.cfg.ErrorThreshold,
		RollbackOnError: c.cfg.RollbackOnError,
		Groups:          make(map[string]Stats),
	}

	c.metrics.mu.Lock()
	for group, s := range c.metrics.requests {
		total := s.total.Load()
		errors := s.errors.Load()
		var errorRate float64
		if total > 0 {
			errorRate = float64(errors) / float64(total)
		}
		var avgLatency float64
		if total > 0 {
			avgLatency = float64(s.totalLatencyNs.Load()) / float64(total) / 1e6
		}
		snap.Groups[group] = Stats{
			Requests:     total,
			Errors:       errors,
			ErrorRate:    errorRate,
			AvgLatencyMs: avgLatency,
		}
	}
	c.metrics.mu.Unlock()

	return snap
}

// Stop terminates the observation goroutine.
func (c *Controller) Stop() {
	if c.stopped.CompareAndSwap(false, true) {
		close(c.stopCh)
	}
}
