package canary

import (
	"sync"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/loadbalancer"
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
