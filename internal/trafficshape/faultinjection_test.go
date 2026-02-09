package trafficshape

import (
	"context"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestFaultInjector_FullAbort(t *testing.T) {
	fi := NewFaultInjector(config.FaultInjectionConfig{
		Enabled: true,
		Abort: config.FaultAbortConfig{
			Percentage: 100,
			StatusCode: 503,
		},
	})

	for i := 0; i < 100; i++ {
		aborted, status := fi.Apply(context.Background())
		if !aborted {
			t.Fatalf("iteration %d: expected abort", i)
		}
		if status != 503 {
			t.Fatalf("iteration %d: expected status 503, got %d", i, status)
		}
	}

	snap := fi.Snapshot()
	if snap.TotalRequests != 100 {
		t.Errorf("expected 100 total requests, got %d", snap.TotalRequests)
	}
	if snap.TotalAborted != 100 {
		t.Errorf("expected 100 aborted, got %d", snap.TotalAborted)
	}
	if snap.TotalDelayed != 0 {
		t.Errorf("expected 0 delayed, got %d", snap.TotalDelayed)
	}
}

func TestFaultInjector_NoFault(t *testing.T) {
	fi := NewFaultInjector(config.FaultInjectionConfig{
		Enabled: true,
		Abort:   config.FaultAbortConfig{Percentage: 0},
		Delay:   config.FaultDelayConfig{Percentage: 0},
	})

	for i := 0; i < 100; i++ {
		aborted, _ := fi.Apply(context.Background())
		if aborted {
			t.Fatalf("iteration %d: unexpected abort", i)
		}
	}

	snap := fi.Snapshot()
	if snap.TotalRequests != 100 {
		t.Errorf("expected 100 total requests, got %d", snap.TotalRequests)
	}
	if snap.TotalAborted != 0 {
		t.Errorf("expected 0 aborted, got %d", snap.TotalAborted)
	}
}

func TestFaultInjector_DelayRespectsCancellation(t *testing.T) {
	fi := NewFaultInjector(config.FaultInjectionConfig{
		Enabled: true,
		Delay: config.FaultDelayConfig{
			Percentage: 100,
			Duration:   5 * time.Second,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	aborted, _ := fi.Apply(ctx)
	elapsed := time.Since(start)

	if aborted {
		t.Error("should not abort, only delay")
	}
	if elapsed > time.Second {
		t.Errorf("delay should have been cancelled early, took %v", elapsed)
	}
}

func TestFaultInjector_DelayAddsLatency(t *testing.T) {
	fi := NewFaultInjector(config.FaultInjectionConfig{
		Enabled: true,
		Delay: config.FaultDelayConfig{
			Percentage: 100,
			Duration:   100 * time.Millisecond,
		},
	})

	start := time.Now()
	aborted, _ := fi.Apply(context.Background())
	elapsed := time.Since(start)

	if aborted {
		t.Error("should not abort")
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("expected at least 80ms delay, got %v", elapsed)
	}

	snap := fi.Snapshot()
	if snap.TotalDelayed != 1 {
		t.Errorf("expected 1 delayed, got %d", snap.TotalDelayed)
	}
	if snap.TotalDelayNs == 0 {
		t.Error("expected non-zero delay nanoseconds")
	}
}

func TestFaultInjector_AbortSkipsDelay(t *testing.T) {
	fi := NewFaultInjector(config.FaultInjectionConfig{
		Enabled: true,
		Abort: config.FaultAbortConfig{
			Percentage: 100,
			StatusCode: 429,
		},
		Delay: config.FaultDelayConfig{
			Percentage: 100,
			Duration:   5 * time.Second,
		},
	})

	start := time.Now()
	aborted, status := fi.Apply(context.Background())
	elapsed := time.Since(start)

	if !aborted {
		t.Error("expected abort")
	}
	if status != 429 {
		t.Errorf("expected status 429, got %d", status)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("abort should not have delayed, took %v", elapsed)
	}
}

func TestFaultInjectionByRoute(t *testing.T) {
	m := NewFaultInjectionByRoute()
	m.AddRoute("r1", config.FaultInjectionConfig{
		Enabled: true,
		Abort:   config.FaultAbortConfig{Percentage: 100, StatusCode: 500},
	})

	if fi := m.GetInjector("r1"); fi == nil {
		t.Fatal("expected non-nil injector for r1")
	}
	if fi := m.GetInjector("r2"); fi != nil {
		t.Fatal("expected nil injector for r2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "r1" {
		t.Errorf("expected [r1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["r1"]; !ok {
		t.Error("expected stats for r1")
	}
}

func TestMergeFaultInjectionConfig(t *testing.T) {
	global := config.FaultInjectionConfig{
		Enabled: true,
		Abort:   config.FaultAbortConfig{Percentage: 10, StatusCode: 503},
		Delay:   config.FaultDelayConfig{Percentage: 20, Duration: 100 * time.Millisecond},
	}

	// Route with its own abort but no delay → inherit global delay
	route := config.FaultInjectionConfig{
		Enabled: true,
		Abort:   config.FaultAbortConfig{Percentage: 50, StatusCode: 429},
	}

	merged := MergeFaultInjectionConfig(route, global)
	if merged.Abort.Percentage != 50 {
		t.Errorf("expected route abort pct 50, got %d", merged.Abort.Percentage)
	}
	if merged.Delay.Percentage != 20 {
		t.Errorf("expected global delay pct 20, got %d", merged.Delay.Percentage)
	}
}
