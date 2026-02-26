package trafficshape

import (
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestAdaptiveLimiter_AllowRelease(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:        true,
		MinConcurrency: 1,
		MaxConcurrency: 10,
	})
	defer al.Stop()

	release, ok := al.Allow()
	if !ok {
		t.Fatal("expected allow")
	}
	if al.inflight.Load() != 1 {
		t.Errorf("expected inflight 1, got %d", al.inflight.Load())
	}
	release(200, 10*time.Millisecond)
	if al.inflight.Load() != 0 {
		t.Errorf("expected inflight 0, got %d", al.inflight.Load())
	}
}

func TestAdaptiveLimiter_RejectAtLimit(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:        true,
		MinConcurrency: 1,
		MaxConcurrency: 2,
	})
	defer al.Stop()

	// Fill up to the limit
	var releases []func(int, time.Duration)
	for i := 0; i < 2; i++ {
		release, ok := al.Allow()
		if !ok {
			t.Fatalf("expected allow on iteration %d", i)
		}
		releases = append(releases, release)
	}

	// Third should be rejected
	_, ok := al.Allow()
	if ok {
		t.Fatal("expected rejection at limit")
	}

	snap := al.Snapshot()
	if snap.TotalRejected != 1 {
		t.Errorf("expected 1 rejected, got %d", snap.TotalRejected)
	}

	// Release one and try again
	releases[0](200, time.Millisecond)
	release, ok := al.Allow()
	if !ok {
		t.Fatal("expected allow after release")
	}
	release(200, time.Millisecond)
	releases[1](200, time.Millisecond)
}

func TestAdaptiveLimiter_OnlySuccessAffectsEWMA(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:        true,
		MinConcurrency: 1,
		MaxConcurrency: 100,
	})
	defer al.Stop()

	// Record a 5xx response — should NOT affect EWMA
	release, _ := al.Allow()
	release(500, 100*time.Millisecond)

	al.mu.Lock()
	samples := al.sampleCount
	al.mu.Unlock()

	if samples != 0 {
		t.Errorf("expected 0 samples after 5xx, got %d", samples)
	}

	// Record a 2xx response — should affect EWMA
	release, _ = al.Allow()
	release(200, 50*time.Millisecond)

	al.mu.Lock()
	samples = al.sampleCount
	al.mu.Unlock()

	if samples != 1 {
		t.Errorf("expected 1 sample after 2xx, got %d", samples)
	}

	// Record a 3xx response — should also affect EWMA
	release, _ = al.Allow()
	release(301, 50*time.Millisecond)

	al.mu.Lock()
	samples = al.sampleCount
	al.mu.Unlock()

	if samples != 2 {
		t.Errorf("expected 2 samples after 3xx, got %d", samples)
	}
}

func TestAdaptiveLimiter_AdditiveIncrease(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:           true,
		MinConcurrency:    1,
		MaxConcurrency:    100,
		LatencyTolerance:  2.0,
		SmoothingFactor:   0.5,
		MinLatencySamples: 1,
	})
	defer al.Stop()

	// Set a consistent low latency so gradient < tolerance
	al.currentLimit.Store(50)
	for i := 0; i < 10; i++ {
		release, _ := al.Allow()
		release(200, 10*time.Millisecond)
	}

	// Trigger manual adjustment
	al.adjust()

	limit := al.currentLimit.Load()
	if limit != 51 {
		t.Errorf("expected additive increase to 51, got %d", limit)
	}
}

func TestAdaptiveLimiter_MultiplicativeDecrease(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:           true,
		MinConcurrency:    1,
		MaxConcurrency:    1000,
		LatencyTolerance:  2.0,
		SmoothingFactor:   1.0, // invalid for config but useful here to set ewma exactly
		MinLatencySamples: 1,
	})
	defer al.Stop()

	// Need smoothing=1 to set EWMA exactly, but config validation rejects it.
	// Use internal state instead.
	al.currentLimit.Store(100)
	al.mu.Lock()
	al.ewmaLatency = float64(30 * time.Millisecond) // 30ms
	al.minLatency = float64(10 * time.Millisecond)  // 10ms, gradient = 3.0 >= 2.0
	al.sampleCount = 100
	al.ewmaInitialized = true
	al.mu.Unlock()

	al.adjust()

	limit := al.currentLimit.Load()
	// newLimit = floor(100 * 10/30) = floor(33.33) = 33
	if limit != 33 {
		t.Errorf("expected multiplicative decrease to 33, got %d", limit)
	}
}

func TestAdaptiveLimiter_ClampToMinMax(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:           true,
		MinConcurrency:    10,
		MaxConcurrency:    50,
		LatencyTolerance:  2.0,
		MinLatencySamples: 1,
	})
	defer al.Stop()

	// Set up for multiplicative decrease that would go below min
	al.currentLimit.Store(12)
	al.mu.Lock()
	al.ewmaLatency = float64(100 * time.Millisecond)
	al.minLatency = float64(10 * time.Millisecond) // gradient = 10
	al.sampleCount = 100
	al.ewmaInitialized = true
	al.mu.Unlock()

	al.adjust()
	limit := al.currentLimit.Load()
	if limit != 10 {
		t.Errorf("expected clamp to min 10, got %d", limit)
	}

	// Set up for additive increase that would go above max
	al.currentLimit.Store(50)
	al.mu.Lock()
	al.ewmaLatency = float64(10 * time.Millisecond)
	al.minLatency = float64(10 * time.Millisecond) // gradient = 1.0 < 2.0
	al.mu.Unlock()

	al.adjust()
	limit = al.currentLimit.Load()
	if limit != 50 {
		t.Errorf("expected clamp to max 50, got %d", limit)
	}
}

func TestAdaptiveLimiter_NotEnoughSamples(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:           true,
		MinConcurrency:    1,
		MaxConcurrency:    100,
		MinLatencySamples: 25,
	})
	defer al.Stop()

	al.currentLimit.Store(50)
	// Record fewer than 25 samples
	for i := 0; i < 10; i++ {
		release, _ := al.Allow()
		release(200, 10*time.Millisecond)
	}

	al.adjust()
	limit := al.currentLimit.Load()
	if limit != 50 {
		t.Errorf("expected no change with insufficient samples, got %d", limit)
	}
}

func TestAdaptiveLimiter_StopLifecycle(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:            true,
		MinConcurrency:     1,
		MaxConcurrency:     100,
		AdjustmentInterval: 10 * time.Millisecond,
	})
	al.Stop()

	// Verify stop completes without hanging — if we got here, it worked
}

func TestAdaptiveLimiter_Snapshot(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled:        true,
		MinConcurrency: 5,
		MaxConcurrency: 100,
	})
	defer al.Stop()

	// Record some requests
	for i := 0; i < 5; i++ {
		release, ok := al.Allow()
		if !ok {
			t.Fatal("unexpected rejection")
		}
		release(200, 10*time.Millisecond)
	}

	snap := al.Snapshot()
	if snap.CurrentLimit != 100 {
		t.Errorf("expected current_limit 100, got %d", snap.CurrentLimit)
	}
	if snap.InFlight != 0 {
		t.Errorf("expected in_flight 0, got %d", snap.InFlight)
	}
	if snap.TotalRequests != 5 {
		t.Errorf("expected 5 total requests, got %d", snap.TotalRequests)
	}
	if snap.TotalAdmitted != 5 {
		t.Errorf("expected 5 admitted, got %d", snap.TotalAdmitted)
	}
	if snap.TotalRejected != 0 {
		t.Errorf("expected 0 rejected, got %d", snap.TotalRejected)
	}
	if snap.Samples != 5 {
		t.Errorf("expected 5 samples, got %d", snap.Samples)
	}
	if snap.EWMALatencyMs <= 0 {
		t.Errorf("expected positive EWMA latency, got %f", snap.EWMALatencyMs)
	}
	if snap.MinLatencyMs <= 0 {
		t.Errorf("expected positive min latency, got %f", snap.MinLatencyMs)
	}
}

func TestAdaptiveConcurrencyByRoute_CRUD(t *testing.T) {
	m := NewAdaptiveConcurrencyByRoute()

	m.AddRoute("r1", config.AdaptiveConcurrencyConfig{
		Enabled:        true,
		MinConcurrency: 1,
		MaxConcurrency: 10,
	})
	m.AddRoute("r2", config.AdaptiveConcurrencyConfig{
		Enabled:        true,
		MinConcurrency: 5,
		MaxConcurrency: 50,
	})

	if l := m.GetLimiter("r1"); l == nil {
		t.Error("expected limiter for r1")
	}
	if l := m.GetLimiter("r2"); l == nil {
		t.Error("expected limiter for r2")
	}
	if l := m.GetLimiter("r3"); l != nil {
		t.Error("expected nil for r3")
	}

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := m.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}

	m.StopAll()
}

func TestMergeAdaptiveConcurrencyConfig(t *testing.T) {
	global := config.AdaptiveConcurrencyConfig{
		MinConcurrency:     5,
		MaxConcurrency:     200,
		LatencyTolerance:   2.0,
		AdjustmentInterval: 10 * time.Second,
		SmoothingFactor:    0.3,
		MinLatencySamples:  50,
	}

	// Route with some overrides
	route := config.AdaptiveConcurrencyConfig{
		Enabled:        true,
		MinConcurrency: 20,
		MaxConcurrency: 0, // should fall back to global
	}

	merged := MergeAdaptiveConcurrencyConfig(route, global)

	if merged.MinConcurrency != 20 {
		t.Errorf("expected MinConcurrency 20, got %d", merged.MinConcurrency)
	}
	if merged.MaxConcurrency != 200 {
		t.Errorf("expected MaxConcurrency 200 from global, got %d", merged.MaxConcurrency)
	}
	if merged.LatencyTolerance != 2.0 {
		t.Errorf("expected LatencyTolerance 2.0 from global, got %f", merged.LatencyTolerance)
	}
	if merged.AdjustmentInterval != 10*time.Second {
		t.Errorf("expected AdjustmentInterval 10s from global, got %v", merged.AdjustmentInterval)
	}
	if merged.SmoothingFactor != 0.3 {
		t.Errorf("expected SmoothingFactor 0.3 from global, got %f", merged.SmoothingFactor)
	}
	if merged.MinLatencySamples != 50 {
		t.Errorf("expected MinLatencySamples 50 from global, got %d", merged.MinLatencySamples)
	}
}

func TestAdaptiveLimiter_DefaultsApplied(t *testing.T) {
	al := NewAdaptiveLimiter(config.AdaptiveConcurrencyConfig{
		Enabled: true,
		// All zero values — defaults should be applied
	})
	defer al.Stop()

	if al.minConcurrency != defaultMinConcurrency {
		t.Errorf("expected default min %d, got %d", defaultMinConcurrency, al.minConcurrency)
	}
	if al.maxConcurrency != defaultMaxConcurrency {
		t.Errorf("expected default max %d, got %d", defaultMaxConcurrency, al.maxConcurrency)
	}
	if al.latencyTolerance != defaultLatencyTolerance {
		t.Errorf("expected default tolerance %f, got %f", defaultLatencyTolerance, al.latencyTolerance)
	}
	if al.currentLimit.Load() != defaultMaxConcurrency {
		t.Errorf("expected initial limit %d, got %d", defaultMaxConcurrency, al.currentLimit.Load())
	}
}
