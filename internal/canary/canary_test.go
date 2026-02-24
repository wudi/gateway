package canary

import (
	"sync"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

func makeBalancer(groups map[string]int) *loadbalancer.WeightedBalancer {
	var splits []config.TrafficSplitConfig
	for name, weight := range groups {
		splits = append(splits, config.TrafficSplitConfig{
			Name:   name,
			Weight: weight,
			Backends: []config.BackendConfig{
				{URL: "http://localhost:8080"},
			},
		})
	}
	return loadbalancer.NewWeightedBalancer(splits)
}

func defaultCanaryCfg() config.CanaryConfig {
	return config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 10, Pause: 50 * time.Millisecond},
			{Weight: 50, Pause: 50 * time.Millisecond},
			{Weight: 100},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold:   0.1,
			LatencyThreshold: 500 * time.Millisecond,
			MinRequests:      5,
			Interval:         20 * time.Millisecond,
		},
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	// Cannot pause from pending
	if err := ctrl.Pause(); err == nil {
		t.Fatal("expected error pausing from pending")
	}

	// Cannot resume from pending
	if err := ctrl.Resume(); err == nil {
		t.Fatal("expected error resuming from pending")
	}

	// Cannot promote from pending
	if err := ctrl.Promote(); err == nil {
		t.Fatal("expected error promoting from pending")
	}

	// Cannot rollback from pending
	if err := ctrl.Rollback(); err == nil {
		t.Fatal("expected error rolling back from pending")
	}
}

func TestStateMachine_Start(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	if ctrl.State() != StatePending {
		t.Fatalf("expected pending, got %s", ctrl.State())
	}

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}
	defer ctrl.Stop()

	if ctrl.State() != StateProgressing {
		t.Fatalf("expected progressing, got %s", ctrl.State())
	}

	// Cannot start again
	if err := ctrl.Start(); err == nil {
		t.Fatal("expected error starting from progressing")
	}
}

func TestWeightRedistribution_TwoGroups(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps:       []config.CanaryStepConfig{{Weight: 50}},
		Analysis:    config.CanaryAnalysisConfig{Interval: time.Hour},
	}
	ctrl := NewController("test", cfg, wb)

	ctrl.adjustWeights(50)
	weights := wb.GetGroupWeights()

	if weights["canary"] != 50 {
		t.Fatalf("expected canary=50, got %d", weights["canary"])
	}
	if weights["stable"] != 50 {
		t.Fatalf("expected stable=50, got %d", weights["stable"])
	}
}

func TestWeightRedistribution_ThreeGroups(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 60, "beta": 30, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps:       []config.CanaryStepConfig{{Weight: 40}},
		Analysis:    config.CanaryAnalysisConfig{Interval: time.Hour},
	}
	ctrl := NewController("test", cfg, wb)

	ctrl.adjustWeights(40)
	weights := wb.GetGroupWeights()

	if weights["canary"] != 40 {
		t.Fatalf("expected canary=40, got %d", weights["canary"])
	}

	// stable and beta should share remaining 60 proportionally (60:30 = 2:1)
	// stable: 60 * 60 / 90 = 40, beta: 60 * 30 / 90 = 20
	total := weights["stable"] + weights["beta"]
	if total != 60 {
		t.Fatalf("expected stable+beta=60, got %d", total)
	}
}

func TestWeightRedistribution_CanaryAt100(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps:       []config.CanaryStepConfig{{Weight: 100}},
		Analysis:    config.CanaryAnalysisConfig{Interval: time.Hour},
	}
	ctrl := NewController("test", cfg, wb)

	ctrl.adjustWeights(100)
	weights := wb.GetGroupWeights()

	if weights["canary"] != 100 {
		t.Fatalf("expected canary=100, got %d", weights["canary"])
	}
	if weights["stable"] != 0 {
		t.Fatalf("expected stable=0, got %d", weights["stable"])
	}
}

func TestWeightRedistribution_CanaryAt0(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps:       []config.CanaryStepConfig{{Weight: 0}},
		Analysis:    config.CanaryAnalysisConfig{Interval: time.Hour},
	}
	ctrl := NewController("test", cfg, wb)

	ctrl.adjustWeights(0)
	weights := wb.GetGroupWeights()

	if weights["canary"] != 0 {
		t.Fatalf("expected canary=0, got %d", weights["canary"])
	}
	if weights["stable"] != 100 {
		t.Fatalf("expected stable=100, got %d", weights["stable"])
	}
}

func TestP99_Calculation(t *testing.T) {
	ring := NewLatencyRing(100)

	// Empty ring
	if p99 := ring.P99(); p99 != 0 {
		t.Fatalf("expected 0 for empty ring, got %v", p99)
	}

	// Add 100 samples: 1ms to 100ms
	for i := 1; i <= 100; i++ {
		ring.Add(time.Duration(i) * time.Millisecond)
	}

	p99 := ring.P99()
	// 99th percentile of [1..100] = index 99 = 100ms
	if p99 != 100*time.Millisecond {
		t.Fatalf("expected p99=100ms, got %v", p99)
	}
}

func TestP99_FewSamples(t *testing.T) {
	ring := NewLatencyRing(1000)

	// Single sample
	ring.Add(42 * time.Millisecond)
	p99 := ring.P99()
	if p99 != 42*time.Millisecond {
		t.Fatalf("expected 42ms for single sample, got %v", p99)
	}
}

func TestRollback_HighErrorRate(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour}, // long pause so we evaluate at step 0
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.1,
			MinRequests:    5,
			Interval:       10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Record high error rate for canary
	for i := 0; i < 10; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	// Wait for the controller to detect and rollback
	time.Sleep(100 * time.Millisecond)

	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected rolled_back, got %s", ctrl.State())
	}

	// Weights should be restored
	weights := wb.GetGroupWeights()
	if weights["stable"] != 90 {
		t.Fatalf("expected stable=90 after rollback, got %d", weights["stable"])
	}
	if weights["canary"] != 10 {
		t.Fatalf("expected canary=10 after rollback, got %d", weights["canary"])
	}

	ctrl.Stop()
}

func TestAdvance_AfterHealthyPause(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 20, Pause: 30 * time.Millisecond},
			{Weight: 50, Pause: 30 * time.Millisecond},
			{Weight: 100},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.5,
			MinRequests:    3,
			Interval:       10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Feed healthy requests
	go func() {
		for i := 0; i < 200; i++ {
			ctrl.RecordRequest("canary", 200, time.Millisecond)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Wait for completion
	deadline := time.After(2 * time.Second)
	for {
		if ctrl.State() == StateCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for completion, state=%s, step=%d", ctrl.State(), ctrl.currentStep)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	ctrl.Stop()
}

func TestPromote_Action(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.Promote(); err != nil {
		t.Fatal(err)
	}

	// Wait for promotion to take effect
	time.Sleep(50 * time.Millisecond)

	if ctrl.State() != StateCompleted {
		t.Fatalf("expected completed after promote, got %s", ctrl.State())
	}

	weights := wb.GetGroupWeights()
	if weights["canary"] != 100 {
		t.Fatalf("expected canary=100 after promote, got %d", weights["canary"])
	}

	ctrl.Stop()
}

func TestRollback_Action(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.Rollback(); err != nil {
		t.Fatal(err)
	}

	// Wait for rollback to take effect
	time.Sleep(50 * time.Millisecond)

	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected rolled_back after rollback, got %s", ctrl.State())
	}

	weights := wb.GetGroupWeights()
	if weights["stable"] != 90 {
		t.Fatalf("expected stable=90 after rollback, got %d", weights["stable"])
	}

	ctrl.Stop()
}

func TestPause_Resume(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.Pause(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	if ctrl.State() != StatePaused {
		t.Fatalf("expected paused, got %s", ctrl.State())
	}

	if err := ctrl.Resume(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	if ctrl.State() != StateProgressing {
		t.Fatalf("expected progressing after resume, got %s", ctrl.State())
	}

	ctrl.Stop()
}

func TestRollback_FromPaused(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.Pause(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	if err := ctrl.Rollback(); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)

	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected rolled_back, got %s", ctrl.State())
	}

	ctrl.Stop()
}

func TestConcurrent_RecordRequest(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctrl.RecordRequest("canary", 200, time.Millisecond)
			ctrl.RecordRequest("stable", 200, time.Millisecond)
		}()
	}
	wg.Wait()

	snap := ctrl.Snapshot()
	if snap.Groups["canary"].Requests != 100 {
		t.Fatalf("expected 100 canary requests, got %d", snap.Groups["canary"].Requests)
	}
	if snap.Groups["stable"].Requests != 100 {
		t.Fatalf("expected 100 stable requests, got %d", snap.Groups["stable"].Requests)
	}
}

func TestCanaryByRoute_Manager(t *testing.T) {
	m := NewCanaryByRoute()

	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()

	if err := m.AddRoute("route1", cfg, wb); err != nil {
		t.Fatal(err)
	}

	if ctrl := m.GetController("route1"); ctrl == nil {
		t.Fatal("expected controller for route1")
	}
	if ctrl := m.GetController("nonexistent"); ctrl != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Fatalf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Fatal("expected stats for route1")
	}

	m.StopAll()
}

func TestSnapshot(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)

	snap := ctrl.Snapshot()
	if snap.State != "pending" {
		t.Fatalf("expected pending, got %s", snap.State)
	}
	if snap.TotalSteps != 3 {
		t.Fatalf("expected 3 total steps, got %d", snap.TotalSteps)
	}
	if snap.CanaryGroup != "canary" {
		t.Fatalf("expected canary group, got %s", snap.CanaryGroup)
	}
	if snap.OriginalWeights["stable"] != 90 {
		t.Fatalf("expected original stable=90, got %d", snap.OriginalWeights["stable"])
	}
}

func TestBaselineGroupDetermination(t *testing.T) {
	// Single non-canary group
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := defaultCanaryCfg()
	ctrl := NewController("test", cfg, wb)
	if ctrl.baselineGroup != "stable" {
		t.Fatalf("expected baseline=stable, got %s", ctrl.baselineGroup)
	}

	// Multiple non-canary groups — pick highest weight
	wb2 := makeBalancer(map[string]int{"stable": 60, "beta": 30, "canary": 10})
	ctrl2 := NewController("test2", cfg, wb2)
	if ctrl2.baselineGroup != "stable" {
		t.Fatalf("expected baseline=stable, got %s", ctrl2.baselineGroup)
	}

	// Tied weights — pick alphabetically first
	wb3 := makeBalancer(map[string]int{"bravo": 45, "alpha": 45, "canary": 10})
	ctrl3 := NewController("test3", cfg, wb3)
	if ctrl3.baselineGroup != "alpha" {
		t.Fatalf("expected baseline=alpha (alphabetical tiebreak), got %s", ctrl3.baselineGroup)
	}
}

func TestComparativeAnalysis_ErrorRateRollback(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			MaxErrorRateIncrease: 1.5,
			MinRequests:          5,
			Interval:             10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Baseline: 2% error rate (2 errors / 100 requests)
	for i := 0; i < 98; i++ {
		ctrl.RecordRequest("stable", 200, time.Millisecond)
	}
	for i := 0; i < 2; i++ {
		ctrl.RecordRequest("stable", 500, time.Millisecond)
	}

	// Canary: 5% error rate = 2.5x baseline → exceeds 1.5x threshold
	for i := 0; i < 95; i++ {
		ctrl.RecordRequest("canary", 200, time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)
	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected rolled_back, got %s", ctrl.State())
	}
	ctrl.Stop()
}

func TestComparativeAnalysis_LatencyRollback(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			MaxLatencyIncrease: 2.0,
			MinRequests:        5,
			Interval:           10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Baseline: ~100ms p99
	for i := 0; i < 100; i++ {
		ctrl.RecordRequest("stable", 200, 100*time.Millisecond)
	}
	// Canary: ~300ms p99 = 3x baseline → exceeds 2.0x threshold
	for i := 0; i < 100; i++ {
		ctrl.RecordRequest("canary", 200, 300*time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)
	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected rolled_back, got %s", ctrl.State())
	}
	ctrl.Stop()
}

func TestComparativeAnalysis_PassesWhenBelowRatio(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			MaxErrorRateIncrease: 2.0,
			MinRequests:          5,
			Interval:             10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Baseline: 4% error rate
	for i := 0; i < 96; i++ {
		ctrl.RecordRequest("stable", 200, time.Millisecond)
	}
	for i := 0; i < 4; i++ {
		ctrl.RecordRequest("stable", 500, time.Millisecond)
	}

	// Canary: 5% error rate = 1.25x baseline → below 2.0x threshold
	for i := 0; i < 95; i++ {
		ctrl.RecordRequest("canary", 200, time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)
	if ctrl.State() != StateProgressing {
		t.Fatalf("expected progressing (within ratio), got %s", ctrl.State())
	}
	ctrl.Stop()
}

func TestComparativeAnalysis_BaselineZeroErrors(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			MaxErrorRateIncrease: 1.5,
			MinRequests:          5,
			Interval:             10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Baseline: 0 errors — comparative check should be skipped
	for i := 0; i < 100; i++ {
		ctrl.RecordRequest("stable", 200, time.Millisecond)
	}
	// Canary: some errors, but comparative is skipped when baseline is 0
	for i := 0; i < 95; i++ {
		ctrl.RecordRequest("canary", 200, time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)
	// Should still be progressing since no absolute threshold set and comparative skipped
	if ctrl.State() != StateProgressing {
		t.Fatalf("expected progressing (baseline zero errors, comparative skipped), got %s", ctrl.State())
	}
	ctrl.Stop()
}

func TestConsecutiveFailures_RollbackAfterN(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.1,
			MaxFailures:    3,
			MinRequests:    5,
			Interval:       50 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Record high error rate for canary
	for i := 0; i < 10; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	// After 1 eval (~50ms), failure count should be 1 but no rollback yet
	time.Sleep(70 * time.Millisecond)
	if ctrl.State() == StateRolledBack {
		t.Fatal("should not rollback after 1 failure when max_failures=3")
	}

	// Wait for remaining evaluations to trigger rollback
	time.Sleep(200 * time.Millisecond)
	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected rolled_back after 3 consecutive failures, got %s", ctrl.State())
	}
	ctrl.Stop()
}

func TestConsecutiveFailures_ResetOnPassingEval(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.5,
			MaxFailures:    5, // high tolerance so we don't rollback during test
			MinRequests:    5,
			Interval:       50 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Start with high error rate (100% errors)
	for i := 0; i < 10; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	// Wait for 1-2 failing evaluations
	time.Sleep(80 * time.Millisecond)
	ctrl.mu.RLock()
	fc := ctrl.failureCount
	ctrl.mu.RUnlock()
	if fc == 0 {
		t.Fatal("expected non-zero failure count")
	}

	// Now flood with good requests to bring error rate well below threshold
	for i := 0; i < 500; i++ {
		ctrl.RecordRequest("canary", 200, time.Millisecond)
	}

	// Wait for a passing evaluation
	time.Sleep(80 * time.Millisecond)
	ctrl.mu.RLock()
	fc = ctrl.failureCount
	ctrl.mu.RUnlock()
	if fc != 0 {
		t.Fatalf("expected failure count reset to 0, got %d", fc)
	}

	if ctrl.State() != StateProgressing {
		t.Fatalf("expected progressing, got %s", ctrl.State())
	}
	ctrl.Stop()
}

func TestConsecutiveFailures_Default(t *testing.T) {
	// max_failures=0 → immediate rollback (backward compat)
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.1,
			MaxFailures:    0, // default — immediate rollback
			MinRequests:    5,
			Interval:       10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 10; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	time.Sleep(100 * time.Millisecond)
	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected immediate rollback with max_failures=0, got %s", ctrl.State())
	}
	ctrl.Stop()
}

func TestConsecutiveFailures_ResetOnStepAdvance(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 20, Pause: 30 * time.Millisecond},
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.5,
			MaxFailures:    5, // high tolerance
			MinRequests:    3,
			Interval:       10 * time.Millisecond,
		},
	}
	ctrl := NewController("test", cfg, wb)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Feed healthy requests to advance through step 0
	go func() {
		for i := 0; i < 200; i++ {
			ctrl.RecordRequest("canary", 200, time.Millisecond)
			ctrl.RecordRequest("stable", 200, time.Millisecond)
			time.Sleep(2 * time.Millisecond)
		}
	}()

	// Wait for step advance
	deadline := time.After(2 * time.Second)
	for {
		ctrl.mu.RLock()
		step := ctrl.currentStep
		ctrl.mu.RUnlock()
		if step == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for step advance")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// After step advance, failure count should be 0
	ctrl.mu.RLock()
	fc := ctrl.failureCount
	ctrl.mu.RUnlock()
	if fc != 0 {
		t.Fatalf("expected failure count reset after step advance, got %d", fc)
	}
	ctrl.Stop()
}

func TestAutoStart(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		AutoStart:   true,
		Steps: []config.CanaryStepConfig{
			{Weight: 20, Pause: 30 * time.Millisecond},
			{Weight: 100},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.5,
			MinRequests:    3,
			Interval:       10 * time.Millisecond,
		},
	}

	m := NewCanaryByRoute()
	if err := m.AddRoute("auto-route", cfg, wb); err != nil {
		t.Fatal(err)
	}

	ctrl := m.GetController("auto-route")

	// Simulate auto-start (as gateway.go does after AddRoute)
	if cfg.AutoStart {
		if err := ctrl.Start(); err != nil {
			t.Fatal(err)
		}
	}

	if ctrl.State() != StateProgressing {
		t.Fatalf("expected progressing after auto-start, got %s", ctrl.State())
	}

	// Feed healthy requests to complete
	go func() {
		for i := 0; i < 200; i++ {
			ctrl.RecordRequest("canary", 200, time.Millisecond)
			time.Sleep(2 * time.Millisecond)
		}
	}()

	deadline := time.After(2 * time.Second)
	for {
		if ctrl.State() == StateCompleted {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for completion, state=%s", ctrl.State())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	ctrl.Stop()
}

func TestSnapshot_NewFields(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps:       []config.CanaryStepConfig{{Weight: 50}},
		Analysis: config.CanaryAnalysisConfig{
			MaxFailures: 3,
			Interval:    time.Hour,
		},
	}
	ctrl := NewController("test", cfg, wb)

	snap := ctrl.Snapshot()
	if snap.BaselineGroup != "stable" {
		t.Fatalf("expected baseline_group=stable, got %s", snap.BaselineGroup)
	}
	if snap.ConsecutiveFailures != 0 {
		t.Fatalf("expected consecutive_failures=0, got %d", snap.ConsecutiveFailures)
	}
	if snap.MaxFailures != 3 {
		t.Fatalf("expected max_failures=3, got %d", snap.MaxFailures)
	}
}

func TestRollbackEvent_ConsecutiveFailures(t *testing.T) {
	wb := makeBalancer(map[string]int{"stable": 90, "canary": 10})
	cfg := config.CanaryConfig{
		Enabled:     true,
		CanaryGroup: "canary",
		Steps: []config.CanaryStepConfig{
			{Weight: 50, Pause: time.Hour},
		},
		Analysis: config.CanaryAnalysisConfig{
			ErrorThreshold: 0.1,
			MaxFailures:    2,
			MinRequests:    5,
			Interval:       10 * time.Millisecond,
		},
	}

	var eventData map[string]interface{}
	var eventMu sync.Mutex

	m := NewCanaryByRoute()
	m.SetOnEvent(func(routeID, eventType string, data map[string]interface{}) {
		if eventType == "canary.rolled_back" {
			eventMu.Lock()
			eventData = data
			eventMu.Unlock()
		}
	})
	if err := m.AddRoute("ev-route", cfg, wb); err != nil {
		t.Fatal(err)
	}

	ctrl := m.GetController("ev-route")
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Record high error rate
	for i := 0; i < 10; i++ {
		ctrl.RecordRequest("canary", 500, time.Millisecond)
	}

	// Wait for rollback (2 consecutive failures)
	time.Sleep(200 * time.Millisecond)
	if ctrl.State() != StateRolledBack {
		t.Fatalf("expected rolled_back, got %s", ctrl.State())
	}

	eventMu.Lock()
	defer eventMu.Unlock()
	if eventData == nil {
		t.Fatal("expected rollback event data")
	}
	fc, ok := eventData["consecutive_failures"]
	if !ok {
		t.Fatal("expected consecutive_failures in event data")
	}
	if fc.(int) != 2 {
		t.Fatalf("expected consecutive_failures=2 in event, got %v", fc)
	}
	ctrl.Stop()
}
