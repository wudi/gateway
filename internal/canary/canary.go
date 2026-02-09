package canary

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/logging"
	"go.uber.org/zap"
)

// CanaryState represents the state of a canary deployment.
type CanaryState string

const (
	StatePending    CanaryState = "pending"
	StateProgressing CanaryState = "progressing"
	StatePaused     CanaryState = "paused"
	StateCompleted  CanaryState = "completed"
	StateRolledBack CanaryState = "rolled_back"
)

// action represents an admin command sent to the background goroutine.
type action int

const (
	actionStart action = iota
	actionPause
	actionResume
	actionPromote
	actionRollback
)

// Controller manages a canary deployment for a single route.
type Controller struct {
	routeID         string
	cfg             config.CanaryConfig
	balancer        *loadbalancer.WeightedBalancer
	state           CanaryState
	currentStep     int
	stepStartedAt   time.Time
	originalWeights map[string]int
	metrics         map[string]*GroupMetrics
	actionCh        chan action
	cancel          context.CancelFunc
	done            chan struct{}
	mu              sync.RWMutex
	onEvent         func(routeID, eventType string, data map[string]interface{})
}

// NewController creates a new canary controller.
func NewController(routeID string, cfg config.CanaryConfig, wb *loadbalancer.WeightedBalancer) *Controller {
	// Snapshot original weights for rollback
	origWeights := wb.GetGroupWeights()

	// Initialize per-group metrics
	gm := make(map[string]*GroupMetrics)
	for name := range origWeights {
		gm[name] = NewGroupMetrics()
	}

	return &Controller{
		routeID:         routeID,
		cfg:             cfg,
		balancer:        wb,
		state:           StatePending,
		originalWeights: origWeights,
		metrics:         gm,
		actionCh:        make(chan action, 1),
		done:            make(chan struct{}),
	}
}

// emitEvent fires the onEvent callback if set.
func (c *Controller) emitEvent(eventType string, data map[string]interface{}) {
	if c.onEvent != nil {
		c.onEvent(c.routeID, eventType, data)
	}
}

// Start transitions from pending to progressing and launches the background goroutine.
func (c *Controller) Start() error {
	c.mu.Lock()
	if c.state != StatePending {
		c.mu.Unlock()
		return fmt.Errorf("cannot start: current state is %s", c.state)
	}
	c.state = StateProgressing
	c.currentStep = 0
	c.stepStartedAt = time.Now()
	c.mu.Unlock()

	// Apply first step weight
	c.adjustWeights(c.cfg.Steps[0].Weight)

	c.emitEvent("canary.started", map[string]interface{}{
		"step": 0, "weight": c.cfg.Steps[0].Weight,
	})

	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	go c.run(ctx)

	return nil
}

// Pause transitions from progressing to paused.
func (c *Controller) Pause() error {
	c.mu.RLock()
	if c.state != StateProgressing {
		c.mu.RUnlock()
		return fmt.Errorf("cannot pause: current state is %s", c.state)
	}
	c.mu.RUnlock()

	c.actionCh <- actionPause
	return nil
}

// Resume transitions from paused to progressing.
func (c *Controller) Resume() error {
	c.mu.RLock()
	if c.state != StatePaused {
		c.mu.RUnlock()
		return fmt.Errorf("cannot resume: current state is %s", c.state)
	}
	c.mu.RUnlock()

	c.actionCh <- actionResume
	return nil
}

// Promote immediately sets canary to 100% and completes.
func (c *Controller) Promote() error {
	c.mu.RLock()
	if c.state != StateProgressing && c.state != StatePaused {
		c.mu.RUnlock()
		return fmt.Errorf("cannot promote: current state is %s", c.state)
	}
	c.mu.RUnlock()

	c.actionCh <- actionPromote
	return nil
}

// Rollback restores original weights.
func (c *Controller) Rollback() error {
	c.mu.RLock()
	if c.state != StateProgressing && c.state != StatePaused {
		c.mu.RUnlock()
		return fmt.Errorf("cannot rollback: current state is %s", c.state)
	}
	c.mu.RUnlock()

	c.actionCh <- actionRollback
	return nil
}

// RecordRequest records a request outcome for the given traffic group.
func (c *Controller) RecordRequest(group string, statusCode int, latency time.Duration) {
	c.mu.RLock()
	gm, ok := c.metrics[group]
	c.mu.RUnlock()
	if ok {
		gm.Record(statusCode, latency)
	}
}

// Stop cancels the background goroutine and waits for it to finish.
func (c *Controller) Stop() {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
}

// State returns the current canary state.
func (c *Controller) State() CanaryState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// Snapshot returns a JSON-serializable view of the canary deployment.
func (c *Controller) Snapshot() CanarySnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	groupSnapshots := make(map[string]GroupSnapshot, len(c.metrics))
	for name, gm := range c.metrics {
		groupSnapshots[name] = gm.Snapshot()
	}

	return CanarySnapshot{
		RouteID:         c.routeID,
		State:           string(c.state),
		CurrentStep:     c.currentStep,
		TotalSteps:      len(c.cfg.Steps),
		CanaryGroup:     c.cfg.CanaryGroup,
		CurrentWeights:  c.balancer.GetGroupWeights(),
		OriginalWeights: c.originalWeights,
		Groups:          groupSnapshots,
	}
}

// CanarySnapshot is a JSON-serializable view of a canary deployment.
type CanarySnapshot struct {
	RouteID         string                   `json:"route_id"`
	State           string                   `json:"state"`
	CurrentStep     int                      `json:"current_step"`
	TotalSteps      int                      `json:"total_steps"`
	CanaryGroup     string                   `json:"canary_group"`
	CurrentWeights  map[string]int           `json:"current_weights"`
	OriginalWeights map[string]int           `json:"original_weights"`
	Groups          map[string]GroupSnapshot `json:"groups"`
}

// run is the background goroutine that manages the canary lifecycle.
func (c *Controller) run(ctx context.Context) {
	defer close(c.done)

	interval := c.cfg.Analysis.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case act := <-c.actionCh:
			switch act {
			case actionPause:
				c.mu.Lock()
				if c.state == StateProgressing {
					c.state = StatePaused
					logging.Info("Canary paused", zap.String("route", c.routeID))
				}
				c.mu.Unlock()
				c.emitEvent("canary.paused", nil)

			case actionResume:
				c.mu.Lock()
				if c.state == StatePaused {
					c.state = StateProgressing
					c.stepStartedAt = time.Now()
					logging.Info("Canary resumed", zap.String("route", c.routeID))
				}
				c.mu.Unlock()
				c.emitEvent("canary.resumed", nil)

			case actionPromote:
				c.adjustWeights(100)
				c.mu.Lock()
				c.state = StateCompleted
				c.mu.Unlock()
				logging.Info("Canary promoted to 100%", zap.String("route", c.routeID))
				c.emitEvent("canary.promoted", nil)
				return

			case actionRollback:
				c.doRollback("admin action")
				return
			}

		case <-ticker.C:
			c.mu.RLock()
			state := c.state
			c.mu.RUnlock()

			if state != StateProgressing {
				continue
			}

			// Evaluate canary group health
			canaryMetrics, ok := c.metrics[c.cfg.CanaryGroup]
			if !ok {
				continue
			}

			// Skip evaluation if not enough requests
			if canaryMetrics.Requests() < int64(c.cfg.Analysis.MinRequests) {
				continue
			}

			// Check error rate
			if c.cfg.Analysis.ErrorThreshold > 0 && canaryMetrics.ErrorRate() > c.cfg.Analysis.ErrorThreshold {
				c.doRollback(fmt.Sprintf("error rate %.2f exceeds threshold %.2f",
					canaryMetrics.ErrorRate(), c.cfg.Analysis.ErrorThreshold))
				return
			}

			// Check p99 latency
			if c.cfg.Analysis.LatencyThreshold > 0 && canaryMetrics.P99() > c.cfg.Analysis.LatencyThreshold {
				c.doRollback(fmt.Sprintf("p99 latency %v exceeds threshold %v",
					canaryMetrics.P99(), c.cfg.Analysis.LatencyThreshold))
				return
			}

			// Check if step pause has elapsed
			c.mu.RLock()
			step := c.cfg.Steps[c.currentStep]
			elapsed := time.Since(c.stepStartedAt)
			currentStep := c.currentStep
			c.mu.RUnlock()

			if step.Pause > 0 && elapsed < step.Pause {
				continue // Still in pause period
			}

			// Advance to next step
			nextStep := currentStep + 1
			if nextStep >= len(c.cfg.Steps) {
				// All steps complete
				c.mu.Lock()
				c.state = StateCompleted
				c.mu.Unlock()
				logging.Info("Canary deployment completed",
					zap.String("route", c.routeID),
				)
				c.emitEvent("canary.completed", nil)
				return
			}

			// Move to next step
			c.mu.Lock()
			c.currentStep = nextStep
			c.stepStartedAt = time.Now()
			c.mu.Unlock()

			// Reset metrics for fresh evaluation at new weight
			canaryMetrics.Reset()

			c.adjustWeights(c.cfg.Steps[nextStep].Weight)
			logging.Info("Canary advanced to next step",
				zap.String("route", c.routeID),
				zap.Int("step", nextStep),
				zap.Int("weight", c.cfg.Steps[nextStep].Weight),
			)
			c.emitEvent("canary.step_advanced", map[string]interface{}{
				"step": nextStep, "weight": c.cfg.Steps[nextStep].Weight,
			})
		}
	}
}

// doRollback restores original weights and transitions to rolled_back.
func (c *Controller) doRollback(reason string) {
	c.balancer.SetGroupWeights(c.originalWeights)
	c.mu.Lock()
	c.state = StateRolledBack
	c.mu.Unlock()
	logging.Warn("Canary rolled back",
		zap.String("route", c.routeID),
		zap.String("reason", reason),
	)
	c.emitEvent("canary.rolled_back", map[string]interface{}{
		"reason": reason,
	})
}

// adjustWeights sets canary group to target weight and distributes remainder proportionally.
func (c *Controller) adjustWeights(canaryWeight int) {
	c.mu.RLock()
	origWeights := c.originalWeights
	canaryGroup := c.cfg.CanaryGroup
	c.mu.RUnlock()

	newWeights := make(map[string]int, len(origWeights))

	// Calculate total original weight for non-canary groups
	nonCanaryTotal := 0
	for name, w := range origWeights {
		if name != canaryGroup {
			nonCanaryTotal += w
		}
	}

	// Set canary group weight
	newWeights[canaryGroup] = canaryWeight
	remainder := 100 - canaryWeight

	if nonCanaryTotal == 0 || remainder == 0 {
		// All weight goes to canary or no other groups
		for name := range origWeights {
			if name != canaryGroup {
				newWeights[name] = 0
			}
		}
	} else {
		// Distribute remainder proportionally based on original weight ratios
		distributed := 0
		var lastName string
		for name, w := range origWeights {
			if name != canaryGroup {
				share := remainder * w / nonCanaryTotal
				newWeights[name] = share
				distributed += share
				lastName = name
			}
		}
		// Last group absorbs rounding remainder
		if lastName != "" && distributed != remainder {
			newWeights[lastName] += remainder - distributed
		}
	}

	c.balancer.SetGroupWeights(newWeights)
}

// CanaryByRoute manages canary controllers per route.
type CanaryByRoute struct {
	controllers map[string]*Controller
	mu          sync.RWMutex
	onEvent     func(routeID, eventType string, data map[string]interface{})
}

// NewCanaryByRoute creates a new CanaryByRoute manager.
func NewCanaryByRoute() *CanaryByRoute {
	return &CanaryByRoute{
		controllers: make(map[string]*Controller),
	}
}

// SetOnEvent registers a callback invoked on canary state transitions.
func (m *CanaryByRoute) SetOnEvent(cb func(routeID, eventType string, data map[string]interface{})) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onEvent = cb
}

// AddRoute adds a canary controller for a route.
func (m *CanaryByRoute) AddRoute(routeID string, cfg config.CanaryConfig, wb *loadbalancer.WeightedBalancer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ctrl := NewController(routeID, cfg, wb)
	ctrl.onEvent = m.onEvent
	m.controllers[routeID] = ctrl
	return nil
}

// GetController returns the controller for a route (may be nil).
func (m *CanaryByRoute) GetController(routeID string) *Controller {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.controllers[routeID]
}

// RouteIDs returns all route IDs with canary controllers.
func (m *CanaryByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.controllers))
	for id := range m.controllers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns snapshots for all canary controllers.
func (m *CanaryByRoute) Stats() map[string]CanarySnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]CanarySnapshot, len(m.controllers))
	for id, ctrl := range m.controllers {
		result[id] = ctrl.Snapshot()
	}
	return result
}

// StopAll stops all controller goroutines.
func (m *CanaryByRoute) StopAll() {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, ctrl := range m.controllers {
		ctrl.Stop()
	}
}
