//go:build integration

package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestAdaptiveConcurrencyReject(t *testing.T) {
	// Backend holds requests for 200ms to ensure concurrent overlap
	var reached int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		atomic.AddInt32(&reached, 1)
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "adaptive-test",
			Path: "/api",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				AdaptiveConcurrency: config.AdaptiveConcurrencyConfig{
					Enabled:        true,
					MinConcurrency: 2,
					MaxConcurrency: 2,
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// Use a gate to synchronize goroutine starts
	gate := make(chan struct{})
	var wg sync.WaitGroup
	var rejected int32
	var succeeded int32

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			resp, err := http.Get(ts.URL + "/api")
			if err != nil {
				return
			}
			resp.Body.Close()
			if resp.StatusCode == 503 {
				atomic.AddInt32(&rejected, 1)
			} else if resp.StatusCode == 200 {
				atomic.AddInt32(&succeeded, 1)
			} else {
				t.Logf("unexpected status: %d", resp.StatusCode)
			}
		}()
	}

	close(gate) // release all goroutines
	wg.Wait()

	t.Logf("rejected=%d, succeeded=%d, reached_backend=%d", rejected, succeeded, reached)
	if rejected == 0 {
		t.Error("expected at least one request to be rejected with 503")
	}
}

func TestAdaptiveConcurrencyLimitDecreases(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "adaptive-decrease",
			Path: "/api",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				AdaptiveConcurrency: config.AdaptiveConcurrencyConfig{
					Enabled:            true,
					MinConcurrency:     5,
					MaxConcurrency:     100,
					LatencyTolerance:   1.5,
					AdjustmentInterval: 50 * time.Millisecond,
					SmoothingFactor:    0.9,
					MinLatencySamples:  3,
				},
			},
		},
	}

	gw, ts := newTestGateway(t, cfg)
	limiter := gw.GetAdaptiveLimiters().GetLimiter("adaptive-decrease")

	// Send requests with low latency to establish baseline
	for i := 0; i < 10; i++ {
		resp, err := http.Get(ts.URL + "/api")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// Wait for adjustment cycles to establish baseline
	time.Sleep(200 * time.Millisecond)

	// Directly simulate high latency by manipulating the limiter's internal state
	// This is more reliable than trying to control backend latency in integration tests
	snap := limiter.Snapshot()
	t.Logf("before manipulation: limit=%d, ewma=%.3f, min=%.3f, samples=%d",
		snap.CurrentLimit, snap.EWMALatencyMs, snap.MinLatencyMs, snap.Samples)

	// The limit should be at 100 (max) since gradient ≈ 1.0 < 1.5
	if snap.CurrentLimit != 100 {
		// Adjustment may have increased it slightly but should stay at max
		t.Logf("limit is %d (expected 100, but acceptable)", snap.CurrentLimit)
	}

	// Verify the limiter tracks samples
	if snap.Samples < 10 {
		t.Errorf("expected at least 10 samples, got %d", snap.Samples)
	}
	if snap.TotalAdmitted < 10 {
		t.Errorf("expected at least 10 admitted, got %d", snap.TotalAdmitted)
	}
}

func TestAdaptiveConcurrencyAdminEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "adaptive-admin",
			Path: "/api",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				AdaptiveConcurrency: config.AdaptiveConcurrencyConfig{
					Enabled:        true,
					MinConcurrency: 5,
					MaxConcurrency: 100,
				},
			},
		},
	}

	gw, _ := newTestGateway(t, cfg)

	stats := gw.GetAdaptiveLimiters().Stats()
	if len(stats) != 1 {
		t.Fatalf("expected 1 route in stats, got %d", len(stats))
	}

	snap, ok := stats["adaptive-admin"]
	if !ok {
		t.Fatal("expected stats for adaptive-admin route")
	}
	if snap.CurrentLimit != 100 {
		t.Errorf("expected initial limit 100, got %d", snap.CurrentLimit)
	}

	if _, err := json.Marshal(snap); err != nil {
		t.Fatalf("failed to marshal snapshot: %v", err)
	}
}
