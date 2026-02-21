package canary

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/logging"
	"go.uber.org/zap"
)

// CanaryState represents the state of a canary deployment.
type CanaryState string

const (
	StatePending     CanaryState = "pending"
	StateProgressing CanaryState = "progressing"
	StatePaused      CanaryState = "paused"
	StateCompleted   CanaryState = "completed"
	StateRolledBack  CanaryState = "rolled_back"
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
	baselineGroup   string // highest-weight non-canary group
	failureCount    int    // consecutive failing evaluations
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

	// Determine baseline group: highest-weight non-canary group, ties broken alphabetically
	var baselineGroup string
	bestWeight := -1
	for name, w := range origWeights {
		if name == cfg.CanaryGroup {
			continue
		}
		if w > bestWeight || (w == bestWeight && name < baselineGroup) {
			baselineGroup = name
			bestWeight = w
		}
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
		baselineGroup:   baselineGroup,
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
		RouteID:             c.routeID,
		State:               string(c.state),
		CurrentStep:         c.currentStep,
		TotalSteps:          len(c.cfg.Steps),
		CanaryGroup:         c.cfg.CanaryGroup,
		BaselineGroup:       c.baselineGroup,
		ConsecutiveFailures: c.failureCount,
		MaxFailures:         c.cfg.Analysis.MaxFailures,
		CurrentWeights:      c.balancer.GetGroupWeights(),
		OriginalWeights:     c.originalWeights,
		Groups:              groupSnapshots,
	}
}

// CanarySnapshot is a JSON-serializable view of a canary deployment.
type CanarySnapshot struct {
	RouteID             string                   `json:"route_id"`
	State               string                   `json:"state"`
	CurrentStep         int                      `json:"current_step"`
	TotalSteps          int                      `json:"total_steps"`
	CanaryGroup         string                   `json:"canary_group"`
	BaselineGroup       string                   `json:"baseline_group"`
	ConsecutiveFailures int                      `json:"consecutive_failures"`
	MaxFailures         int                      `json:"max_failures"`
	CurrentWeights      map[string]int           `json:"current_weights"`
	OriginalWeights     map[string]int           `json:"original_weights"`
	Groups              map[string]GroupSnapshot `json:"groups"`
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

			// Determine max consecutive failures before rollback (0 means immediate)
			maxFailures := c.cfg.Analysis.MaxFailures
			if maxFailures <= 0 {
				maxFailures = 1
			}

			failed := false
			var failReason string

			// 1. Absolute threshold checks
			if c.cfg.Analysis.ErrorThreshold > 0 && canaryMetrics.ErrorRate() > c.cfg.Analysis.ErrorThreshold {
				failed = true
				failReason = fmt.Sprintf("error rate %.4f exceeds threshold %.4f",
					canaryMetrics.ErrorRate(), c.cfg.Analysis.ErrorThreshold)
			}
			if !failed && c.cfg.Analysis.LatencyThreshold > 0 && canaryMetrics.P99() > c.cfg.Analysis.LatencyThreshold {
				failed = true
				failReason = fmt.Sprintf("p99 latency %v exceeds threshold %v",
					canaryMetrics.P99(), c.cfg.Analysis.LatencyThreshold)
			}

			// 2. Comparative checks (canary vs baseline)
			if !failed && c.baselineGroup != "" {
				if baselineMetrics, bOK := c.metrics[c.baselineGroup]; bOK {
					// Error rate comparison
					if c.cfg.Analysis.MaxErrorRateIncrease > 0 {
						baselineErr := baselineMetrics.ErrorRate()
						canaryErr := canaryMetrics.ErrorRate()
						if baselineErr > 0 && canaryErr/baselineErr > c.cfg.Analysis.MaxErrorRateIncrease {
							failed = true
							failReason = fmt.Sprintf("canary error rate %.4f is %.2fx baseline %.4f (threshold %.2fx)",
								canaryErr, canaryErr/baselineErr, baselineErr, c.cfg.Analysis.MaxErrorRateIncrease)
						}
					}
					// Latency comparison
					if !failed && c.cfg.Analysis.MaxLatencyIncrease > 0 {
						baselineP99 := baselineMetrics.P99()
						canaryP99 := canaryMetrics.P99()
						if baselineP99 > 0 && float64(canaryP99)/float64(baselineP99) > c.cfg.Analysis.MaxLatencyIncrease {
							failed = true
							failReason = fmt.Sprintf("canary p99 %v is %.2fx baseline %v (threshold %.2fx)",
								canaryP99, float64(canaryP99)/float64(baselineP99), baselineP99, c.cfg.Analysis.MaxLatencyIncrease)
						}
					}
				}
			}

			// 3. Handle failure / success
			if failed {
				c.mu.Lock()
				c.failureCount++
				fc := c.failureCount
				c.mu.Unlock()

				if fc >= maxFailures {
					c.doRollback(failReason)
					return
				}
				logging.Warn("Canary evaluation failed (within tolerance)",
					zap.String("route", c.routeID),
					zap.String("reason", failReason),
					zap.Int("consecutive_failures", fc),
					zap.Int("max_failures", maxFailures),
				)
				continue
			}

			// Passed — reset failure counter
			c.mu.Lock()
			c.failureCount = 0
			c.mu.Unlock()

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
			c.failureCount = 0
			c.mu.Unlock()

			// Reset metrics for fresh evaluation at new weight
			canaryMetrics.Reset()
			if baselineMetrics, bOK := c.metrics[c.baselineGroup]; bOK {
				baselineMetrics.Reset()
			}

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
	fc := c.failureCount
	c.mu.Unlock()
	logging.Warn("Canary rolled back",
		zap.String("route", c.routeID),
		zap.String("reason", reason),
		zap.Int("consecutive_failures", fc),
	)
	c.emitEvent("canary.rolled_back", map[string]interface{}{
		"reason":               reason,
		"consecutive_failures": fc,
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
	byroute.Manager[*Controller]
	eventMu sync.RWMutex
	onEvent func(routeID, eventType string, data map[string]interface{})
}

// NewCanaryByRoute creates a new CanaryByRoute manager.
func NewCanaryByRoute() *CanaryByRoute {
	return &CanaryByRoute{}
}

// SetOnEvent registers a callback invoked on canary state transitions.
func (m *CanaryByRoute) SetOnEvent(cb func(routeID, eventType string, data map[string]interface{})) {
	m.eventMu.Lock()
	defer m.eventMu.Unlock()
	m.onEvent = cb
}

// AddRoute adds a canary controller for a route.
func (m *CanaryByRoute) AddRoute(routeID string, cfg config.CanaryConfig, wb *loadbalancer.WeightedBalancer) error {
	ctrl := NewController(routeID, cfg, wb)
	m.eventMu.RLock()
	ctrl.onEvent = m.onEvent
	m.eventMu.RUnlock()
	m.Add(routeID, ctrl)
	return nil
}

// GetController returns the controller for a route (may be nil).
func (m *CanaryByRoute) GetController(routeID string) *Controller {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns snapshots for all canary controllers.
func (m *CanaryByRoute) Stats() map[string]CanarySnapshot {
	return byroute.CollectStats(&m.Manager, func(ctrl *Controller) CanarySnapshot { return ctrl.Snapshot() })
}

// StopAll stops all controller goroutines.
func (m *CanaryByRoute) StopAll() {
	m.Range(func(_ string, ctrl *Controller) bool {
		ctrl.Stop()
		return true
	})
}
