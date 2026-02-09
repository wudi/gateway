package rules

import (
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/wudi/gateway/internal/config"
)

// CompiledRule is a pre-compiled expression rule ready for evaluation.
type CompiledRule struct {
	ID         string
	Expression string
	program    *vm.Program
	Action     Action
	Enabled    bool
}

// Action defines what happens when a rule matches.
type Action struct {
	Type        string // block, custom_response, redirect, set_headers, rewrite, group, log
	StatusCode  int
	Body        string
	RedirectURL string
	Headers     config.HeaderTransform
	Rewrite     *config.RewriteActionConfig // rewrite action config
	Group       string                      // traffic split group name
	LogMessage  string                      // optional log message
}

// IsTerminating returns true for actions that end request processing.
func IsTerminating(action Action) bool {
	switch action.Type {
	case "block", "custom_response", "redirect":
		return true
	default:
		return false
	}
}

// CompileRequestRule compiles a rule config for the request phase.
func CompileRequestRule(cfg config.RuleConfig) (*CompiledRule, error) {
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}

	program, err := expr.Compile(cfg.Expression, expr.Env(RequestEnv{}), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("rule %s: failed to compile expression: %w", cfg.ID, err)
	}

	return &CompiledRule{
		ID:         cfg.ID,
		Expression: cfg.Expression,
		program:    program,
		Action:     actionFromConfig(cfg),
		Enabled:    enabled,
	}, nil
}

// CompileResponseRule compiles a rule config for the response phase.
func CompileResponseRule(cfg config.RuleConfig) (*CompiledRule, error) {
	enabled := true
	if cfg.Enabled != nil {
		enabled = *cfg.Enabled
	}

	program, err := expr.Compile(cfg.Expression, expr.Env(ResponseEnv{}), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("rule %s: failed to compile expression: %w", cfg.ID, err)
	}

	return &CompiledRule{
		ID:         cfg.ID,
		Expression: cfg.Expression,
		program:    program,
		Action:     actionFromConfig(cfg),
		Enabled:    enabled,
	}, nil
}

// Evaluate runs the compiled program against the given environment.
func (cr *CompiledRule) Evaluate(env any) (bool, error) {
	output, err := expr.Run(cr.program, env)
	if err != nil {
		return false, err
	}
	result, ok := output.(bool)
	if !ok {
		return false, fmt.Errorf("rule %s: expression did not return bool", cr.ID)
	}
	return result, nil
}

func actionFromConfig(cfg config.RuleConfig) Action {
	return Action{
		Type:        cfg.Action,
		StatusCode:  cfg.StatusCode,
		Body:        cfg.Body,
		RedirectURL: cfg.RedirectURL,
		Headers:     cfg.Headers,
		Rewrite:     cfg.Rewrite,
		Group:       cfg.Group,
		LogMessage:  cfg.LogMessage,
	}
}
