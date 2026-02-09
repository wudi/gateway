//go:build integration
// +build integration

package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestThrottleIntegration(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "throttled",
			Path: "/api",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				Throttle: config.ThrottleConfig{
					Enabled: true,
					Rate:    100,
					Burst:   5,
					MaxWait: 2 * time.Second,
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// Send a burst of requests — they should succeed (possibly with delays)
	var wg sync.WaitGroup
	var successes atomic.Int64
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(ts.URL + "/api")
			if err != nil {
				return
			}
			defer resp.Body.Close()
			io.ReadAll(resp.Body)
			if resp.StatusCode == 200 {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()

	// With rate=100/s and burst=5, most requests should succeed
	if successes.Load() < 5 {
		t.Errorf("expected at least 5 successes, got %d", successes.Load())
	}
}

func TestThrottleTimeout(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "slow",
			Path: "/slow",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				Throttle: config.ThrottleConfig{
					Enabled: true,
					Rate:    1,
					Burst:   1,
					MaxWait: 50 * time.Millisecond,
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// First request uses burst token
	resp, err := http.Get(ts.URL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("first request: expected 200, got %d", resp.StatusCode)
	}

	// Rapid second request should timeout (1/s rate, 50ms max wait)
	resp2, err := http.Get(ts.URL + "/slow")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 503 {
		t.Errorf("throttled request: expected 503, got %d", resp2.StatusCode)
	}
}

func TestBandwidthIntegration(t *testing.T) {
	// Backend sends a large response
	payload := strings.Repeat("x", 10240) // 10KB
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(payload))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "bw-limited",
			Path: "/data",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				Bandwidth: config.BandwidthConfig{
					Enabled:      true,
					ResponseRate: 5120, // 5KB/s — should take ~2s for 10KB
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	start := time.Now()
	resp, err := http.Get(ts.URL + "/data")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	elapsed := time.Since(start)

	if len(body) != len(payload) {
		t.Errorf("expected %d bytes, got %d", len(payload), len(body))
	}

	// Should take at least 1 second for 10KB at 5KB/s
	if elapsed < 500*time.Millisecond {
		t.Errorf("bandwidth limiting too fast: elapsed=%v", elapsed)
	}
}

func TestPriorityIntegration(t *testing.T) {
	var activeCount atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeCount.Add(1)
		defer activeCount.Add(-1)
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.TrafficShaping = config.TrafficShapingConfig{
		Priority: config.PriorityConfig{
			Enabled:       true,
			MaxConcurrent: 2,
			MaxWait:       5 * time.Second,
			DefaultLevel:  5,
			Levels: []config.PriorityLevelConfig{
				{Level: 1, Headers: map[string]string{"X-Priority": "high"}},
			},
		},
	}
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "priority-route",
			Path: "/priority",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// Send requests with mixed priorities
	var wg sync.WaitGroup
	completionOrder := make([]string, 0)
	var mu sync.Mutex

	// Fill 2 concurrent slots
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _ := http.Get(ts.URL + "/priority")
			if resp != nil {
				resp.Body.Close()
			}
			mu.Lock()
			completionOrder = append(completionOrder, "filler")
			mu.Unlock()
		}()
	}

	// Give fillers time to start
	time.Sleep(10 * time.Millisecond)

	// Queue a high-priority request
	wg.Add(1)
	go func() {
		defer wg.Done()
		req, _ := http.NewRequest("GET", ts.URL+"/priority", nil)
		req.Header.Set("X-Priority", "high")
		resp, _ := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		mu.Lock()
		completionOrder = append(completionOrder, "high")
		mu.Unlock()
	}()

	// Queue a low-priority request
	wg.Add(1)
	go func() {
		defer wg.Done()
		resp, _ := http.Get(ts.URL + "/priority")
		if resp != nil {
			resp.Body.Close()
		}
		mu.Lock()
		completionOrder = append(completionOrder, "low")
		mu.Unlock()
	}()

	wg.Wait()

	// All should complete
	if len(completionOrder) != 4 {
		t.Errorf("expected 4 completions, got %d", len(completionOrder))
	}
}

func TestTrafficShapingAdminEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`ok`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Admin = config.AdminConfig{Enabled: true, Port: 0}
	cfg.TrafficShaping = config.TrafficShapingConfig{
		Priority: config.PriorityConfig{
			Enabled:       true,
			MaxConcurrent: 100,
			DefaultLevel:  5,
		},
	}
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "admin-test",
			Path: "/test",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				Throttle: config.ThrottleConfig{
					Enabled: true,
					Rate:    100,
					Burst:   10,
				},
			},
		},
	}

	gw, _ := newTestGateway(t, cfg)

	// Verify the admin stats return properly
	throttleStats := gw.GetThrottlers().Stats()
	if _, ok := throttleStats["admin-test"]; !ok {
		t.Error("expected throttle stats for admin-test route")
	}

	pa := gw.GetPriorityAdmitter()
	if pa == nil {
		t.Fatal("expected priority admitter to be non-nil")
	}
	snap := pa.Snapshot()
	if snap.MaxConcurrent != 100 {
		t.Errorf("expected max_concurrent=100, got %d", snap.MaxConcurrent)
	}
}

func TestTrafficShapingConfigValidation(t *testing.T) {
	loader := config.NewLoader()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "throttle rate zero",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
    traffic_shaping:
      throttle:
        enabled: true
        rate: 0
`,
			wantErr: "throttle rate must be > 0",
		},
		{
			name: "priority max_concurrent zero",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
traffic_shaping:
  priority:
    enabled: true
    max_concurrent: 0
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
`,
			wantErr: "priority max_concurrent must be > 0",
		},
		{
			name: "priority level out of range",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
traffic_shaping:
  priority:
    enabled: true
    max_concurrent: 10
    levels:
      - level: 15
        headers:
          X-VIP: "true"
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
`,
			wantErr: "level must be between 1 and 10",
		},
		{
			name: "per-route priority without global",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
    traffic_shaping:
      priority:
        enabled: true
        max_concurrent: 10
`,
			wantErr: "per-route priority requires global priority to be enabled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loader.Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// Suppress unused import warnings
var _ = json.Marshal
