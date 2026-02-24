package rules

import (
	"fmt"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	lua "github.com/yuin/gopher-lua"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/luautil"
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
	Type        string // block, custom_response, redirect, set_headers, rewrite, group, log, delay, set_var, set_status, set_body, cache_bypass, lua
	StatusCode  int
	Body        string
	RedirectURL string
	Headers     config.HeaderTransform
	Rewrite     *config.RewriteActionConfig // rewrite action config
	Group       string                      // traffic split group name
	LogMessage  string                      // optional log message
	LuaProto    *lua.FunctionProto          // pre-compiled Lua for lua action
	Delay       time.Duration               // delay duration for delay action
	Variables   map[string]string           // key-value pairs for set_var action
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

	action, err := actionFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("rule %s: %w", cfg.ID, err)
	}

	return &CompiledRule{
		ID:         cfg.ID,
		Expression: cfg.Expression,
		program:    program,
		Action:     action,
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

	action, err := actionFromConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("rule %s: %w", cfg.ID, err)
	}

	return &CompiledRule{
		ID:         cfg.ID,
		Expression: cfg.Expression,
		program:    program,
		Action:     action,
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

func actionFromConfig(cfg config.RuleConfig) (Action, error) {
	var luaProto *lua.FunctionProto
	if cfg.Action == "lua" && cfg.LuaScript != "" {
		proto, err := luautil.CompileScript(cfg.LuaScript, "rule-"+cfg.ID)
		if err != nil {
			return Action{}, fmt.Errorf("failed to compile lua_script: %w", err)
		}
		luaProto = proto
	}

	return Action{
		Type:        cfg.Action,
		StatusCode:  cfg.StatusCode,
		Body:        cfg.Body,
		RedirectURL: cfg.RedirectURL,
		Headers:     cfg.Headers,
		Rewrite:     cfg.Rewrite,
		Group:       cfg.Group,
		LogMessage:  cfg.LogMessage,
		LuaProto:    luaProto,
		Delay:       cfg.Delay,
		Variables:   cfg.Variables,
	}, nil
}
