package abtest

import (
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

func newTestWB(groups ...string) *loadbalancer.WeightedBalancer {
	splits := make([]config.TrafficSplitConfig, len(groups))
	for i, name := range groups {
		splits[i] = config.TrafficSplitConfig{
			Name:   name,
			Weight: 50,
			Backends: []config.BackendConfig{
				{URL: "http://localhost:8080"},
			},
		}
	}
	return loadbalancer.NewWeightedBalancer(splits)
}

func TestRecordAndSnapshot(t *testing.T) {
	wb := newTestWB("control", "experiment")
	ab := New("route1", config.ABTestConfig{
		Enabled:        true,
		ExperimentName: "test-exp",
	}, wb)

	// Record some requests.
	ab.RecordRequest("control", 200, 10*time.Millisecond)
	ab.RecordRequest("control", 200, 20*time.Millisecond)
	ab.RecordRequest("control", 500, 30*time.Millisecond)
	ab.RecordRequest("experiment", 200, 5*time.Millisecond)
	ab.RecordRequest("experiment", 200, 15*time.Millisecond)

	snap := ab.Snapshot()

	if snap.ExperimentName != "test-exp" {
		t.Fatalf("expected experiment name 'test-exp', got %q", snap.ExperimentName)
	}
	if snap.RouteID != "route1" {
		t.Fatalf("expected route ID 'route1', got %q", snap.RouteID)
	}
	if len(snap.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(snap.Groups))
	}

	ctrl := snap.Groups["control"]
	if ctrl.Requests != 3 {
		t.Fatalf("expected control requests=3, got %d", ctrl.Requests)
	}
	if ctrl.Errors != 1 {
		t.Fatalf("expected control errors=1, got %d", ctrl.Errors)
	}

	exp := snap.Groups["experiment"]
	if exp.Requests != 2 {
		t.Fatalf("expected experiment requests=2, got %d", exp.Requests)
	}
	if exp.Errors != 0 {
		t.Fatalf("expected experiment errors=0, got %d", exp.Errors)
	}
}

func TestReset(t *testing.T) {
	wb := newTestWB("a", "b")
	ab := New("r1", config.ABTestConfig{
		Enabled:        true,
		ExperimentName: "exp1",
	}, wb)

	ab.RecordRequest("a", 200, 10*time.Millisecond)
	ab.RecordRequest("b", 500, 20*time.Millisecond)

	ab.Reset()

	snap := ab.Snapshot()
	if snap.Groups["a"].Requests != 0 {
		t.Fatalf("expected 0 requests after reset, got %d", snap.Groups["a"].Requests)
	}
	if snap.Groups["b"].Requests != 0 {
		t.Fatalf("expected 0 requests after reset, got %d", snap.Groups["b"].Requests)
	}
	if snap.DurationSec > 1 {
		t.Fatal("expected duration near 0 after reset")
	}
}

func TestUnknownGroupNoOp(t *testing.T) {
	wb := newTestWB("a")
	ab := New("r1", config.ABTestConfig{
		Enabled:        true,
		ExperimentName: "exp1",
	}, wb)

	// Should not panic.
	ab.RecordRequest("nonexistent", 200, 10*time.Millisecond)

	snap := ab.Snapshot()
	if snap.Groups["a"].Requests != 0 {
		t.Fatalf("expected 0 requests for group 'a', got %d", snap.Groups["a"].Requests)
	}
}

func TestByRouteManager(t *testing.T) {
	wb := newTestWB("control", "experiment")
	mgr := NewABTestByRoute()
	mgr.AddRoute("route1", config.ABTestConfig{
		Enabled:        true,
		ExperimentName: "exp1",
	}, wb)

	ab := mgr.GetTest("route1")
	if ab == nil {
		t.Fatal("expected A/B test for route1")
	}
	if ab.experimentName != "exp1" {
		t.Fatalf("expected experiment name 'exp1', got %q", ab.experimentName)
	}

	if mgr.GetTest("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	stats := mgr.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Fatal("expected stats for route1")
	}
}
