package rules

import (
	"fmt"
	"sync"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/logging"
	"go.uber.org/zap"
)

// Result is the outcome of evaluating a single rule.
type Result struct {
	Matched    bool
	Terminated bool
	Action     Action
	RuleID     string
}

// RuleEngine holds compiled request and response rules with metrics.
type RuleEngine struct {
	requestRules  []*CompiledRule
	responseRules []*CompiledRule
	metrics       *Metrics
}

// NewEngine compiles all request and response rules from config.
func NewEngine(reqCfgs, respCfgs []config.RuleConfig) (*RuleEngine, error) {
	e := &RuleEngine{
		metrics: &Metrics{},
	}

	for _, cfg := range reqCfgs {
		cr, err := CompileRequestRule(cfg)
		if err != nil {
			return nil, err
		}
		e.requestRules = append(e.requestRules, cr)
	}

	for _, cfg := range respCfgs {
		cr, err := CompileResponseRule(cfg)
		if err != nil {
			return nil, err
		}
		e.responseRules = append(e.responseRules, cr)
	}

	return e, nil
}

// EvaluateRequest evaluates request-phase rules in order.
// Stops on first terminating match.
func (e *RuleEngine) EvaluateRequest(env RequestEnv) []Result {
	return e.evaluate(e.requestRules, env)
}

// EvaluateResponse evaluates response-phase rules in order.
// Stops on first terminating match.
func (e *RuleEngine) EvaluateResponse(env ResponseEnv) []Result {
	return e.evaluate(e.responseRules, env)
}

func (e *RuleEngine) evaluate(rules []*CompiledRule, env any) []Result {
	var results []Result

	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		e.metrics.Evaluated.Add(1)

		matched, err := rule.Evaluate(env)
		if err != nil {
			e.metrics.Errors.Add(1)
			logging.Error("rule evaluation error", zap.String("rule_id", rule.ID), zap.Error(err))
			continue
		}

		if !matched {
			continue
		}

		e.metrics.Matched.Add(1)
		terminated := IsTerminating(rule.Action)
		if terminated {
			e.metrics.Blocked.Add(1)
		}

		results = append(results, Result{
			Matched:    true,
			Terminated: terminated,
			Action:     rule.Action,
			RuleID:     rule.ID,
		})

		if terminated {
			break
		}
	}

	return results
}

// HasRequestRules returns true if request-phase rules exist.
func (e *RuleEngine) HasRequestRules() bool {
	return len(e.requestRules) > 0
}

// HasResponseRules returns true if response-phase rules exist.
func (e *RuleEngine) HasResponseRules() bool {
	return len(e.responseRules) > 0
}

// GetMetrics returns the metrics snapshot.
func (e *RuleEngine) GetMetrics() MetricsSnapshot {
	return e.metrics.Snapshot()
}

// RuleInfo holds metadata about a compiled rule for the admin API.
type RuleInfo struct {
	ID         string `json:"id"`
	Expression string `json:"expression"`
	Action     string `json:"action"`
	Enabled    bool   `json:"enabled"`
}

// RequestRuleInfos returns metadata about all request rules.
func (e *RuleEngine) RequestRuleInfos() []RuleInfo {
	return ruleInfos(e.requestRules)
}

// ResponseRuleInfos returns metadata about all response rules.
func (e *RuleEngine) ResponseRuleInfos() []RuleInfo {
	return ruleInfos(e.responseRules)
}

func ruleInfos(rules []*CompiledRule) []RuleInfo {
	infos := make([]RuleInfo, len(rules))
	for i, r := range rules {
		infos[i] = RuleInfo{
			ID:         r.ID,
			Expression: r.Expression,
			Action:     r.Action.Type,
			Enabled:    r.Enabled,
		}
	}
	return infos
}

// RulesByRoute manages per-route rule engines.
type RulesByRoute struct {
	engines map[string]*RuleEngine
	mu      sync.RWMutex
}

// NewRulesByRoute creates a new per-route rule manager.
func NewRulesByRoute() *RulesByRoute {
	return &RulesByRoute{
		engines: make(map[string]*RuleEngine),
	}
}

// AddRoute compiles and stores rules for a route.
func (rbr *RulesByRoute) AddRoute(routeID string, rules config.RulesConfig) error {
	engine, err := NewEngine(rules.Request, rules.Response)
	if err != nil {
		return fmt.Errorf("route %s: %w", routeID, err)
	}

	rbr.mu.Lock()
	rbr.engines[routeID] = engine
	rbr.mu.Unlock()

	return nil
}

// GetEngine returns the rule engine for a route, or nil.
func (rbr *RulesByRoute) GetEngine(routeID string) *RuleEngine {
	rbr.mu.RLock()
	defer rbr.mu.RUnlock()
	return rbr.engines[routeID]
}

// EngineStats is the admin API view of one rule engine.
type EngineStats struct {
	RequestRules  []RuleInfo      `json:"request_rules"`
	ResponseRules []RuleInfo      `json:"response_rules"`
	Metrics       MetricsSnapshot `json:"metrics"`
}

// RouteIDs returns all route IDs with rule engines.
func (rbr *RulesByRoute) RouteIDs() []string {
	rbr.mu.RLock()
	defer rbr.mu.RUnlock()
	ids := make([]string, 0, len(rbr.engines))
	for id := range rbr.engines {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns admin API info for all routes.
func (rbr *RulesByRoute) Stats() map[string]EngineStats {
	rbr.mu.RLock()
	defer rbr.mu.RUnlock()

	result := make(map[string]EngineStats, len(rbr.engines))
	for routeID, engine := range rbr.engines {
		result[routeID] = EngineStats{
			RequestRules:  engine.RequestRuleInfos(),
			ResponseRules: engine.ResponseRuleInfos(),
			Metrics:       engine.GetMetrics(),
		}
	}
	return result
}
