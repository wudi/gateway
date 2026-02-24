package bluegreen

import (
	"testing"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/health"
	"github.com/wudi/gateway/internal/loadbalancer"
)

func newTestController(cfg config.BlueGreenConfig) (*Controller, *loadbalancer.WeightedBalancer) {
	splits := []config.TrafficSplitConfig{
		{
			Name:     "blue",
			Weight:   100,
			Backends: []config.BackendConfig{{URL: "http://blue:8080"}},
		},
		{
			Name:     "green",
			Weight:   0,
			Backends: []config.BackendConfig{{URL: "http://green:8080"}},
		},
	}
	wb := loadbalancer.NewWeightedBalancer(splits)
	hc := health.NewChecker(health.Config{DefaultInterval: 10 * time.Second, DefaultTimeout: 2 * time.Second})
	ctrl := NewController("test-route", cfg, wb, hc)
	return ctrl, wb
}

func TestNewController_InitialState(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	snap := ctrl.Snapshot()
	if snap.State != StateInactive {
		t.Errorf("expected inactive state, got %s", snap.State)
	}
	if snap.ActiveGroup != "blue" {
		t.Errorf("expected blue active, got %s", snap.ActiveGroup)
	}
	if snap.InactiveGroup != "green" {
		t.Errorf("expected green inactive, got %s", snap.InactiveGroup)
	}
}

func TestPromote_FromInactive(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, wb := newTestController(cfg)
	defer ctrl.Stop()

	if err := ctrl.Promote(); err != nil {
		t.Fatalf("promote failed: %v", err)
	}

	snap := ctrl.Snapshot()
	if snap.State != StatePromoting {
		t.Errorf("expected promoting state, got %s", snap.State)
	}

	// Verify weights changed: green=100, blue=0
	weights := wb.GetGroupWeights()
	if weights["green"] != 100 {
		t.Errorf("expected green weight 100, got %d", weights["green"])
	}
	if weights["blue"] != 0 {
		t.Errorf("expected blue weight 0, got %d", weights["blue"])
	}
}

func TestPromote_InvalidState(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	// Promote to get into promoting state
	ctrl.Promote()

	// Try promoting again from promoting state — should fail
	if err := ctrl.Promote(); err == nil {
		t.Error("expected error when promoting from promoting state")
	}
}

func TestRollback(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, wb := newTestController(cfg)
	defer ctrl.Stop()

	// Promote first
	ctrl.Promote()

	// Rollback
	if err := ctrl.Rollback(); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	snap := ctrl.Snapshot()
	if snap.State != StateRolledBack {
		t.Errorf("expected rolled_back state, got %s", snap.State)
	}

	// Weights should be restored
	weights := wb.GetGroupWeights()
	if weights["blue"] != 100 {
		t.Errorf("expected blue weight 100 after rollback, got %d", weights["blue"])
	}
}

func TestRollback_InvalidState(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	// Try rollback from inactive — should fail
	if err := ctrl.Rollback(); err == nil {
		t.Error("expected error when rolling back from inactive state")
	}
}

func TestRecordRequest(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	ctrl.RecordRequest("blue", 200, time.Millisecond)
	ctrl.RecordRequest("blue", 500, time.Millisecond)
	ctrl.RecordRequest("green", 200, time.Millisecond)

	snap := ctrl.Snapshot()
	if snap.Groups["blue"].Requests != 2 {
		t.Errorf("expected 2 blue requests, got %d", snap.Groups["blue"].Requests)
	}
	if snap.Groups["blue"].Errors != 1 {
		t.Errorf("expected 1 blue error, got %d", snap.Groups["blue"].Errors)
	}
	if snap.Groups["green"].Requests != 1 {
		t.Errorf("expected 1 green request, got %d", snap.Groups["green"].Requests)
	}
}

func TestObservation_AutoRollback(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:           true,
		ActiveGroup:       "blue",
		InactiveGroup:     "green",
		RollbackOnError:   true,
		ErrorThreshold:    0.5,
		ObservationWindow: 5 * time.Second,
		MinRequests:       2,
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	// Promote
	ctrl.Promote()

	// Simulate high error rate on the promoted group (green)
	ctrl.RecordRequest("green", 500, time.Millisecond)
	ctrl.RecordRequest("green", 500, time.Millisecond)
	ctrl.RecordRequest("green", 500, time.Millisecond)

	// Wait for observation goroutine to detect and rollback
	time.Sleep(2 * time.Second)

	snap := ctrl.Snapshot()
	if snap.State != StateRolledBack {
		t.Errorf("expected auto-rollback due to high error rate, got state %s", snap.State)
	}
}

func TestObservation_PromotesToActive(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:           true,
		ActiveGroup:       "blue",
		InactiveGroup:     "green",
		RollbackOnError:   true,
		ErrorThreshold:    0.5,
		ObservationWindow: 1 * time.Second,
		MinRequests:       1,
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	// Promote
	ctrl.Promote()

	// Record successful requests
	ctrl.RecordRequest("green", 200, time.Millisecond)
	ctrl.RecordRequest("green", 200, time.Millisecond)

	// Wait for observation window to pass
	time.Sleep(2 * time.Second)

	snap := ctrl.Snapshot()
	if snap.State != StateActive {
		t.Errorf("expected active state after observation window, got %s", snap.State)
	}
}

func TestPromote_AfterRollback(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	// Promote, then rollback
	ctrl.Promote()
	ctrl.Rollback()

	// Should be able to promote again from rolled_back state
	if err := ctrl.Promote(); err != nil {
		t.Fatalf("expected re-promote from rolled_back to succeed: %v", err)
	}

	snap := ctrl.Snapshot()
	if snap.State != StatePromoting {
		t.Errorf("expected promoting state, got %s", snap.State)
	}
}

func TestStop_Idempotent(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, _ := newTestController(cfg)

	// Should not panic when called multiple times
	ctrl.Stop()
	ctrl.Stop()
}

func TestSnapshot_ErrorRate(t *testing.T) {
	cfg := config.BlueGreenConfig{
		Enabled:       true,
		ActiveGroup:   "blue",
		InactiveGroup: "green",
	}
	ctrl, _ := newTestController(cfg)
	defer ctrl.Stop()

	ctrl.RecordRequest("blue", 200, 10*time.Millisecond)
	ctrl.RecordRequest("blue", 200, 20*time.Millisecond)
	ctrl.RecordRequest("blue", 500, 30*time.Millisecond)

	snap := ctrl.Snapshot()
	stats := snap.Groups["blue"]

	expectedRate := 1.0 / 3.0
	if stats.ErrorRate < expectedRate-0.01 || stats.ErrorRate > expectedRate+0.01 {
		t.Errorf("expected error rate ~%.2f, got %.2f", expectedRate, stats.ErrorRate)
	}

	if stats.AvgLatencyMs < 10 || stats.AvgLatencyMs > 30 {
		t.Errorf("unexpected avg latency: %.2f ms", stats.AvgLatencyMs)
	}
}
