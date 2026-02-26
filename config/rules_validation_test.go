package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRules_SkipActions_RequestPhase(t *testing.T) {
	l := NewLoader()
	skipActions := []string{
		"skip_auth", "skip_rate_limit", "skip_throttle", "skip_circuit_breaker",
		"skip_waf", "skip_validation", "skip_compression", "skip_adaptive_concurrency",
		"skip_body_limit", "skip_mirror", "skip_access_log", "skip_cache_store",
	}
	unsafeActions := map[string]bool{
		"skip_auth": true, "skip_waf": true, "skip_body_limit": true,
	}

	for _, action := range skipActions {
		t.Run(action+"_accepted", func(t *testing.T) {
			rules := []RuleConfig{{
				ID:         "r1",
				Expression: "true",
				Action:     action,
				Unsafe:     unsafeActions[action],
			}}
			if err := l.validateRules(rules, "request"); err != nil {
				t.Errorf("expected no error for %s in request phase, got: %v", action, err)
			}
		})
	}
}

func TestValidateRules_SkipActions_ResponsePhase_Rejected(t *testing.T) {
	l := NewLoader()
	requestOnly := []string{
		"skip_auth", "skip_rate_limit", "skip_throttle", "skip_circuit_breaker",
		"skip_waf", "skip_validation", "skip_compression", "skip_adaptive_concurrency",
		"skip_body_limit", "skip_mirror", "skip_access_log",
	}

	for _, action := range requestOnly {
		t.Run(action+"_response_rejected", func(t *testing.T) {
			rules := []RuleConfig{{
				ID:         "r1",
				Expression: "true",
				Action:     action,
				Unsafe:     true,
			}}
			err := l.validateRules(rules, "response")
			if err == nil {
				t.Errorf("expected error for %s in response phase", action)
			}
		})
	}
}

func TestValidateRules_SkipCacheStore_BothPhases(t *testing.T) {
	l := NewLoader()
	rule := RuleConfig{ID: "r1", Expression: "true", Action: "skip_cache_store"}

	if err := l.validateRules([]RuleConfig{rule}, "request"); err != nil {
		t.Errorf("skip_cache_store should be valid in request phase: %v", err)
	}
	if err := l.validateRules([]RuleConfig{rule}, "response"); err != nil {
		t.Errorf("skip_cache_store should be valid in response phase: %v", err)
	}
}

func TestValidateRules_UnsafeGating(t *testing.T) {
	l := NewLoader()
	unsafeActions := []string{"skip_auth", "skip_waf", "skip_body_limit"}

	for _, action := range unsafeActions {
		t.Run(action+"_without_unsafe", func(t *testing.T) {
			rules := []RuleConfig{{
				ID:         "r1",
				Expression: "true",
				Action:     action,
				Unsafe:     false,
			}}
			err := l.validateRules(rules, "request")
			if err == nil {
				t.Errorf("expected error for %s without unsafe: true", action)
			}
			if !strings.Contains(err.Error(), "requires unsafe: true") {
				t.Errorf("error should mention unsafe: %v", err)
			}
		})

		t.Run(action+"_with_unsafe", func(t *testing.T) {
			rules := []RuleConfig{{
				ID:         "r1",
				Expression: "true",
				Action:     action,
				Unsafe:     true,
			}}
			if err := l.validateRules(rules, "request"); err != nil {
				t.Errorf("expected no error for %s with unsafe: true, got: %v", action, err)
			}
		})
	}
}

func TestValidateRules_SkipActionsRejectParams(t *testing.T) {
	l := NewLoader()
	rules := []RuleConfig{{
		ID:         "r1",
		Expression: "true",
		Action:     "skip_rate_limit",
		Params:     map[string]string{"tier": "x"},
	}}
	err := l.validateRules(rules, "request")
	if err == nil {
		t.Error("expected error for skip action with params")
	}
}

func TestValidateRules_OverrideParamValidation(t *testing.T) {
	l := NewLoader()

	tests := []struct {
		name    string
		rule    RuleConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "rate_limit_tier_valid",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "rate_limit_tier",
				Params: map[string]string{"tier": "premium"},
			},
			wantErr: false,
		},
		{
			name: "rate_limit_tier_missing_param",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "rate_limit_tier",
			},
			wantErr: true, errMsg: "requires params.tier",
		},
		{
			name: "timeout_override_valid",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "timeout_override",
				Params: map[string]string{"timeout": "5s"},
			},
			wantErr: false,
		},
		{
			name: "timeout_override_invalid_duration",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "timeout_override",
				Params: map[string]string{"timeout": "not-a-duration"},
			},
			wantErr: true, errMsg: "positive duration",
		},
		{
			name: "priority_override_valid",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "priority_override",
				Params: map[string]string{"priority": "5"},
			},
			wantErr: false,
		},
		{
			name: "priority_override_out_of_range",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "priority_override",
				Params: map[string]string{"priority": "15"},
			},
			wantErr: true, errMsg: "1-10",
		},
		{
			name: "bandwidth_override_valid",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "bandwidth_override",
				Params: map[string]string{"bandwidth": "1048576"},
			},
			wantErr: false,
		},
		{
			name: "bandwidth_override_negative",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "bandwidth_override",
				Params: map[string]string{"bandwidth": "-100"},
			},
			wantErr: true, errMsg: "positive integer",
		},
		{
			name: "body_limit_override_valid",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "body_limit_override",
				Params: map[string]string{"body_limit": "2097152"},
			},
			wantErr: false,
		},
		{
			name: "switch_backend_valid",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "switch_backend",
				Params: map[string]string{"backend": "http://backend-1:8080"},
			},
			wantErr: false,
		},
		{
			name: "switch_backend_missing",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "switch_backend",
			},
			wantErr: true, errMsg: "requires params.backend",
		},
		{
			name: "cache_ttl_override_valid",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "cache_ttl_override",
				Params: map[string]string{"cache_ttl": "2m"},
			},
			wantErr: false,
		},
		{
			name: "cache_ttl_override_in_request_phase",
			rule: RuleConfig{
				ID: "r1", Expression: "true", Action: "cache_ttl_override",
				Params: map[string]string{"cache_ttl": "2m"},
			},
			wantErr: true, errMsg: "only allowed in response phase",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := "request"
			if tt.rule.Action == "cache_ttl_override" && tt.name == "cache_ttl_override_valid" {
				phase = "response"
			}
			if tt.name == "cache_ttl_override_in_request_phase" {
				phase = "request"
			}
			err := l.validateRules([]RuleConfig{tt.rule}, phase)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if tt.wantErr && err != nil && tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error should contain %q, got: %v", tt.errMsg, err)
			}
		})
	}
}

func TestValidateOverrideCaps(t *testing.T) {
	l := NewLoader()

	t.Run("timeout_exceeds_route", func(t *testing.T) {
		rules := []RuleConfig{{
			ID: "r1", Expression: "true", Action: "timeout_override",
			Params: map[string]string{"timeout": "30s"},
		}}
		route := &RouteConfig{
			ID:      "test-route",
			Timeout: 10 * time.Second,
		}
		err := l.validateOverrideCaps(rules, route, &Config{})
		if err == nil {
			t.Error("expected error: timeout exceeds route limit")
		}
	})

	t.Run("timeout_within_route", func(t *testing.T) {
		rules := []RuleConfig{{
			ID: "r1", Expression: "true", Action: "timeout_override",
			Params: map[string]string{"timeout": "5s"},
		}}
		route := &RouteConfig{
			ID:      "test-route",
			Timeout: 10 * time.Second,
		}
		if err := l.validateOverrideCaps(rules, route, &Config{}); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("body_limit_exceeds_2x", func(t *testing.T) {
		rules := []RuleConfig{{
			ID: "r1", Expression: "true", Action: "body_limit_override",
			Params: map[string]string{"body_limit": "30000000"},
		}}
		route := &RouteConfig{
			ID:          "test-route",
			MaxBodySize: 10000000,
		}
		err := l.validateOverrideCaps(rules, route, &Config{})
		if err == nil {
			t.Error("expected error: body limit exceeds 2x")
		}
	})

	t.Run("switch_backend_in_pool", func(t *testing.T) {
		rules := []RuleConfig{{
			ID: "r1", Expression: "true", Action: "switch_backend",
			Params: map[string]string{"backend": "http://b1:8080"},
		}}
		route := &RouteConfig{
			ID:       "test-route",
			Backends: []BackendConfig{{URL: "http://b1:8080"}, {URL: "http://b2:8080"}},
		}
		if err := l.validateOverrideCaps(rules, route, &Config{}); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})

	t.Run("switch_backend_not_in_pool", func(t *testing.T) {
		rules := []RuleConfig{{
			ID: "r1", Expression: "true", Action: "switch_backend",
			Params: map[string]string{"backend": "http://unknown:8080"},
		}}
		route := &RouteConfig{
			ID:       "test-route",
			Backends: []BackendConfig{{URL: "http://b1:8080"}},
		}
		err := l.validateOverrideCaps(rules, route, &Config{})
		if err == nil {
			t.Error("expected error: backend not in pool")
		}
	})

	t.Run("switch_backend_in_upstream_pool", func(t *testing.T) {
		rules := []RuleConfig{{
			ID: "r1", Expression: "true", Action: "switch_backend",
			Params: map[string]string{"backend": "http://up-b1:8080"},
		}}
		route := &RouteConfig{
			ID:       "test-route",
			Upstream: "my-upstream",
		}
		cfg := &Config{
			Upstreams: map[string]UpstreamConfig{
				"my-upstream": {
					Backends: []BackendConfig{{URL: "http://up-b1:8080"}},
				},
			},
		}
		if err := l.validateOverrideCaps(rules, route, cfg); err != nil {
			t.Errorf("expected no error, got: %v", err)
		}
	})
}
