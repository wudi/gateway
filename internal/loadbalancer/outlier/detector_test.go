package outlier

import (
	"sync"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
)

func newTestBalancer(urls ...string) *loadbalancer.RoundRobin {
	var backends []*loadbalancer.Backend
	for _, u := range urls {
		backends = append(backends, &loadbalancer.Backend{URL: u, Weight: 1, Healthy: true})
	}
	return loadbalancer.NewRoundRobin(backends)
}

func testConfig() config.OutlierDetectionConfig {
	return config.OutlierDetectionConfig{
		Enabled:              true,
		Interval:             50 * time.Millisecond, // fast for tests
		Window:               5 * time.Second,
		MinRequests:          3,
		ErrorRateThreshold:   0.5,
		ErrorRateMultiplier:  1.5,
		LatencyMultiplier:    3.0,
		BaseEjectionDuration: 100 * time.Millisecond,
		MaxEjectionDuration:  500 * time.Millisecond,
		MaxEjectionPercent:   50,
	}
}

func TestDetectorEjectsErrorOutlier(t *testing.T) {
	bal := newTestBalancer("http://good1:8080", "http://good2:8080", "http://bad:8080")
	cfg := testConfig()

	var ejected sync.Map
	d := NewDetector("test-route", cfg, bal)
	d.SetCallbacks(
		func(routeID, backend, reason string) { ejected.Store(backend, reason) },
		nil,
	)
	defer d.Stop()

	// Record good requests for good backends
	for i := 0; i < 10; i++ {
		d.Record("http://good1:8080", 200, 5*time.Millisecond)
		d.Record("http://good2:8080", 200, 5*time.Millisecond)
	}

	// Record all errors for bad backend
	for i := 0; i < 10; i++ {
		d.Record("http://bad:8080", 500, 5*time.Millisecond)
	}

	// Wait for detection cycle
	time.Sleep(200 * time.Millisecond)

	if _, ok := ejected.Load("http://bad:8080"); !ok {
		t.Error("expected bad backend to be ejected")
	}

	// Verify backend is marked unhealthy
	for _, b := range bal.GetBackends() {
		if b.URL == "http://bad:8080" && b.Healthy {
			t.Error("expected bad backend to be marked unhealthy")
		}
	}
}

func TestDetectorRecovery(t *testing.T) {
	bal := newTestBalancer("http://good:8080", "http://bad:8080")
	cfg := testConfig()
	cfg.BaseEjectionDuration = 100 * time.Millisecond
	cfg.Window = 500 * time.Millisecond // short window so old errors expire

	var recovered sync.Map
	d := NewDetector("test-route", cfg, bal)
	d.SetCallbacks(
		nil,
		func(routeID, backend string) { recovered.Store(backend, true) },
	)
	defer d.Stop()

	// Record enough to trigger ejection
	for i := 0; i < 10; i++ {
		d.Record("http://good:8080", 200, 5*time.Millisecond)
		d.Record("http://bad:8080", 500, 5*time.Millisecond)
	}

	// Wait for ejection
	time.Sleep(200 * time.Millisecond)

	// Wait for old stats to expire from the sliding window
	time.Sleep(600 * time.Millisecond)

	// Record good data for the previously bad backend so it won't be re-ejected
	for i := 0; i < 5; i++ {
		d.Record("http://good:8080", 200, 5*time.Millisecond)
		d.Record("http://bad:8080", 200, 5*time.Millisecond)
	}

	// Wait for another detection cycle to process recovery
	time.Sleep(200 * time.Millisecond)

	if _, ok := recovered.Load("http://bad:8080"); !ok {
		t.Error("expected bad backend to be recovered")
	}

	// Verify backend is marked healthy again
	for _, b := range bal.GetBackends() {
		if b.URL == "http://bad:8080" && !b.Healthy {
			t.Error("expected bad backend to be marked healthy after recovery")
		}
	}
}

func TestDetectorSkipsTooFewBackends(t *testing.T) {
	bal := newTestBalancer("http://only:8080")
	cfg := testConfig()

	d := NewDetector("test-route", cfg, bal)
	defer d.Stop()

	// Record all errors for only backend
	for i := 0; i < 10; i++ {
		d.Record("http://only:8080", 500, 5*time.Millisecond)
	}

	// Wait for detection cycle
	time.Sleep(200 * time.Millisecond)

	// Should not eject the only backend
	for _, b := range bal.GetBackends() {
		if !b.Healthy {
			t.Error("should not eject when only 1 eligible backend")
		}
	}
}

func TestDetectorMaxEjectionPercent(t *testing.T) {
	bal := newTestBalancer("http://bad1:8080", "http://bad2:8080", "http://bad3:8080", "http://good:8080")
	cfg := testConfig()
	cfg.MaxEjectionPercent = 25 // max 1 out of 4

	d := NewDetector("test-route", cfg, bal)
	defer d.Stop()

	// All backends error except good
	for i := 0; i < 10; i++ {
		d.Record("http://good:8080", 200, 5*time.Millisecond)
		d.Record("http://bad1:8080", 500, 5*time.Millisecond)
		d.Record("http://bad2:8080", 500, 5*time.Millisecond)
		d.Record("http://bad3:8080", 500, 5*time.Millisecond)
	}

	time.Sleep(200 * time.Millisecond)

	snap := d.Snapshot()
	if len(snap.EjectedBackends) > 1 {
		t.Errorf("expected at most 1 ejection due to max 25%%, got %d", len(snap.EjectedBackends))
	}
}

func TestDetectorSnapshot(t *testing.T) {
	bal := newTestBalancer("http://a:8080", "http://b:8080")
	cfg := testConfig()

	d := NewDetector("test-route", cfg, bal)
	defer d.Stop()

	d.Record("http://a:8080", 200, 5*time.Millisecond)
	d.Record("http://b:8080", 500, 10*time.Millisecond)

	snap := d.Snapshot()
	if snap.RouteID != "test-route" {
		t.Errorf("expected route_id=test-route, got %s", snap.RouteID)
	}
	if len(snap.BackendStats) != 2 {
		t.Errorf("expected 2 backend stats, got %d", len(snap.BackendStats))
	}
}

func TestManagerLifecycle(t *testing.T) {
	m := NewDetectorByRoute()

	bal := newTestBalancer("http://a:8080", "http://b:8080")
	cfg := testConfig()

	m.AddRoute("route1", cfg, bal)
	m.AddRoute("route2", cfg, bal)

	if d := m.Lookup("route1"); d == nil {
		t.Error("expected detector for route1")
	}
	if d := m.Lookup("nonexistent"); d != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := m.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats, got %d", len(stats))
	}

	m.StopAll()
	if len(m.RouteIDs()) != 0 {
		t.Error("expected empty after StopAll")
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := config.OutlierDetectionConfig{Enabled: true}
	applyDefaults(&cfg)

	if cfg.Interval != 10*time.Second {
		t.Errorf("expected default interval 10s, got %v", cfg.Interval)
	}
	if cfg.Window != 30*time.Second {
		t.Errorf("expected default window 30s, got %v", cfg.Window)
	}
	if cfg.MinRequests != 10 {
		t.Errorf("expected default min_requests 10, got %d", cfg.MinRequests)
	}
	if cfg.ErrorRateThreshold != 0.5 {
		t.Errorf("expected default error_rate_threshold 0.5, got %f", cfg.ErrorRateThreshold)
	}
	if cfg.MaxEjectionPercent != 50 {
		t.Errorf("expected default max_ejection_percent 50, got %f", cfg.MaxEjectionPercent)
	}
}

func TestMedianFloat64(t *testing.T) {
	tests := []struct {
		vals []float64
		want float64
	}{
		{[]float64{1, 2, 3}, 2},
		{[]float64{1, 2, 3, 4}, 2.5},
		{[]float64{5}, 5},
		{nil, 0},
	}
	for _, tt := range tests {
		got := medianFloat64(tt.vals)
		if got != tt.want {
			t.Errorf("medianFloat64(%v) = %v, want %v", tt.vals, got, tt.want)
		}
	}
}

func TestMedianDuration(t *testing.T) {
	vals := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	got := medianDuration(vals)
	if got != 20*time.Millisecond {
		t.Errorf("expected 20ms, got %v", got)
	}
}
