package rules

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/variables"
)

func TestExecuteSkip(t *testing.T) {
	tests := []struct {
		name string
		flag variables.SkipFlags
	}{
		{"SkipAuth", variables.SkipAuth},
		{"SkipRateLimit", variables.SkipRateLimit},
		{"SkipThrottle", variables.SkipThrottle},
		{"SkipCircuitBreaker", variables.SkipCircuitBreaker},
		{"SkipWAF", variables.SkipWAF},
		{"SkipValidation", variables.SkipValidation},
		{"SkipCompression", variables.SkipCompression},
		{"SkipAdaptiveConcurrency", variables.SkipAdaptiveConcurrency},
		{"SkipBodyLimit", variables.SkipBodyLimit},
		{"SkipMirror", variables.SkipMirror},
		{"SkipAccessLog", variables.SkipAccessLog},
		{"SkipCacheStore", variables.SkipCacheStore},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			varCtx := &variables.Context{}
			ExecuteSkip(varCtx, tt.flag)
			if varCtx.SkipFlags&tt.flag == 0 {
				t.Errorf("expected flag %v to be set", tt.flag)
			}
		})
	}
}

func TestExecuteSkip_MultipleFlags(t *testing.T) {
	varCtx := &variables.Context{}
	ExecuteSkip(varCtx, variables.SkipAuth)
	ExecuteSkip(varCtx, variables.SkipRateLimit)
	ExecuteSkip(varCtx, variables.SkipWAF)

	if varCtx.SkipFlags&variables.SkipAuth == 0 {
		t.Error("expected SkipAuth to be set")
	}
	if varCtx.SkipFlags&variables.SkipRateLimit == 0 {
		t.Error("expected SkipRateLimit to be set")
	}
	if varCtx.SkipFlags&variables.SkipWAF == 0 {
		t.Error("expected SkipWAF to be set")
	}
	if varCtx.SkipFlags&variables.SkipThrottle != 0 {
		t.Error("expected SkipThrottle to NOT be set")
	}
}

func TestExecuteOverrides_LazyInit(t *testing.T) {
	varCtx := &variables.Context{}
	if varCtx.Overrides != nil {
		t.Fatal("expected Overrides to be nil initially")
	}
	ExecuteRateLimitTier(varCtx, "premium")
	if varCtx.Overrides == nil {
		t.Fatal("expected Overrides to be non-nil after setting tier")
	}
	if varCtx.Overrides.RateLimitTier != "premium" {
		t.Errorf("expected tier 'premium', got %q", varCtx.Overrides.RateLimitTier)
	}
}

func TestExecuteOverrides_AllTypes(t *testing.T) {
	varCtx := &variables.Context{}

	ExecuteRateLimitTier(varCtx, "premium")
	ExecuteTimeoutOverride(varCtx, 5*time.Second)
	ExecutePriorityOverride(varCtx, 3)
	ExecuteBandwidthOverride(varCtx, 1048576)
	ExecuteBodyLimitOverride(varCtx, 2097152)
	ExecuteSwitchBackend(varCtx, "http://backend-2:8080")
	ExecuteCacheTTLOverride(varCtx, 30*time.Second)

	ov := varCtx.Overrides
	if ov.RateLimitTier != "premium" {
		t.Errorf("RateLimitTier = %q, want 'premium'", ov.RateLimitTier)
	}
	if ov.TimeoutOverride != 5*time.Second {
		t.Errorf("TimeoutOverride = %v, want 5s", ov.TimeoutOverride)
	}
	if ov.PriorityOverride != 3 {
		t.Errorf("PriorityOverride = %d, want 3", ov.PriorityOverride)
	}
	if ov.BandwidthOverride != 1048576 {
		t.Errorf("BandwidthOverride = %d, want 1048576", ov.BandwidthOverride)
	}
	if ov.BodyLimitOverride != 2097152 {
		t.Errorf("BodyLimitOverride = %d, want 2097152", ov.BodyLimitOverride)
	}
	if ov.SwitchBackend != "http://backend-2:8080" {
		t.Errorf("SwitchBackend = %q, want 'http://backend-2:8080'", ov.SwitchBackend)
	}
	if ov.CacheTTLOverride != 30*time.Second {
		t.Errorf("CacheTTLOverride = %v, want 30s", ov.CacheTTLOverride)
	}
}

func TestOverrideLastWriterWins(t *testing.T) {
	varCtx := &variables.Context{}
	ExecuteRateLimitTier(varCtx, "basic")
	ExecuteRateLimitTier(varCtx, "premium")
	if varCtx.Overrides.RateLimitTier != "premium" {
		t.Errorf("expected last-writer-wins: got %q, want 'premium'", varCtx.Overrides.RateLimitTier)
	}
}

func TestActionCounts(t *testing.T) {
	engine, err := NewEngine(
		[]config.RuleConfig{
			{
				ID:         "skip-auth",
				Expression: `true`,
				Action:     "skip_auth",
				Unsafe:     true,
			},
			{
				ID:         "set-header",
				Expression: `true`,
				Action:     "set_headers",
				Headers:    config.HeaderTransform{Set: map[string]string{"X-Test": "val"}},
			},
		},
		nil,
	)
	if err != nil {
		t.Fatalf("engine creation error: %v", err)
	}

	r := httptest.NewRequest("GET", "http://localhost/", nil)
	env := NewRequestEnv(r, nil)
	engine.EvaluateRequest(&env)
	engine.EvaluateRequest(&env)

	snap := engine.GetMetrics()
	if snap.ActionCounts["skip_auth"] != 2 {
		t.Errorf("skip_auth count = %d, want 2", snap.ActionCounts["skip_auth"])
	}
	if snap.ActionCounts["set_headers"] != 2 {
		t.Errorf("set_headers count = %d, want 2", snap.ActionCounts["set_headers"])
	}
	if snap.ActionCounts["skip_rate_limit"] != 0 {
		// Zero counts are omitted from snapshot
		t.Errorf("skip_rate_limit should be 0 in snapshot")
	}
}

func TestActionFromConfig_ParsesOverrideParams(t *testing.T) {
	tests := []struct {
		name   string
		cfg    config.RuleConfig
		check  func(t *testing.T, a Action)
	}{
		{
			name: "rate_limit_tier",
			cfg: config.RuleConfig{
				ID: "t1", Expression: "true", Action: "rate_limit_tier",
				Params: map[string]string{"tier": "gold"},
			},
			check: func(t *testing.T, a Action) {
				if a.Tier != "gold" {
					t.Errorf("Tier = %q, want 'gold'", a.Tier)
				}
			},
		},
		{
			name: "timeout_override",
			cfg: config.RuleConfig{
				ID: "t2", Expression: "true", Action: "timeout_override",
				Params: map[string]string{"timeout": "5s"},
			},
			check: func(t *testing.T, a Action) {
				if a.Timeout != 5*time.Second {
					t.Errorf("Timeout = %v, want 5s", a.Timeout)
				}
			},
		},
		{
			name: "priority_override",
			cfg: config.RuleConfig{
				ID: "t3", Expression: "true", Action: "priority_override",
				Params: map[string]string{"priority": "7"},
			},
			check: func(t *testing.T, a Action) {
				if a.Priority != 7 {
					t.Errorf("Priority = %d, want 7", a.Priority)
				}
			},
		},
		{
			name: "bandwidth_override",
			cfg: config.RuleConfig{
				ID: "t4", Expression: "true", Action: "bandwidth_override",
				Params: map[string]string{"bandwidth": "1048576"},
			},
			check: func(t *testing.T, a Action) {
				if a.Bandwidth != 1048576 {
					t.Errorf("Bandwidth = %d, want 1048576", a.Bandwidth)
				}
			},
		},
		{
			name: "body_limit_override",
			cfg: config.RuleConfig{
				ID: "t5", Expression: "true", Action: "body_limit_override",
				Params: map[string]string{"body_limit": "2097152"},
			},
			check: func(t *testing.T, a Action) {
				if a.BodyLimit != 2097152 {
					t.Errorf("BodyLimit = %d, want 2097152", a.BodyLimit)
				}
			},
		},
		{
			name: "switch_backend",
			cfg: config.RuleConfig{
				ID: "t6", Expression: "true", Action: "switch_backend",
				Params: map[string]string{"backend": "http://special:8080"},
			},
			check: func(t *testing.T, a Action) {
				if a.Backend != "http://special:8080" {
					t.Errorf("Backend = %q, want 'http://special:8080'", a.Backend)
				}
			},
		},
		{
			name: "cache_ttl_override",
			cfg: config.RuleConfig{
				ID: "t7", Expression: "true", Action: "cache_ttl_override",
				Params: map[string]string{"cache_ttl": "2m"},
			},
			check: func(t *testing.T, a Action) {
				if a.CacheTTL != 2*time.Minute {
					t.Errorf("CacheTTL = %v, want 2m", a.CacheTTL)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := actionFromConfig(tt.cfg)
			if err != nil {
				t.Fatalf("actionFromConfig error: %v", err)
			}
			tt.check(t, a)
		})
	}
}

func TestContextClonePreservesOverrides(t *testing.T) {
	varCtx := &variables.Context{}
	ExecuteSkip(varCtx, variables.SkipAuth)
	ExecuteRateLimitTier(varCtx, "premium")

	clone := varCtx.Clone()

	if clone.SkipFlags&variables.SkipAuth == 0 {
		t.Error("clone should have SkipAuth flag")
	}
	if clone.Overrides == nil || clone.Overrides.RateLimitTier != "premium" {
		t.Error("clone should have RateLimitTier = premium")
	}

	// Ensure clone is independent
	clone.Overrides.RateLimitTier = "basic"
	if varCtx.Overrides.RateLimitTier != "premium" {
		t.Error("modifying clone should not affect original")
	}
}

func TestContextReleaseResetsOverrides(t *testing.T) {
	varCtx := variables.AcquireContext(httptest.NewRequest("GET", "/", nil))
	ExecuteSkip(varCtx, variables.SkipAuth)
	ExecuteRateLimitTier(varCtx, "premium")

	variables.ReleaseContext(varCtx)
	// After release, fields should be zeroed (pool reuse will verify)
}
