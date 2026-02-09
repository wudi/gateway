//go:build integration
// +build integration

package test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestFaultInjectionAbort(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "fault-abort",
			Path: "/abort",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				FaultInjection: config.FaultInjectionConfig{
					Enabled: true,
					Abort: config.FaultAbortConfig{
						Percentage: 100,
						StatusCode: 503,
					},
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	resp, err := http.Get(ts.URL + "/abort")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 503 {
		t.Errorf("expected 503, got %d", resp.StatusCode)
	}
}

func TestFaultInjectionDelay(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "fault-delay",
			Path: "/delay",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				FaultInjection: config.FaultInjectionConfig{
					Enabled: true,
					Delay: config.FaultDelayConfig{
						Percentage: 100,
						Duration:   100 * time.Millisecond,
					},
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	start := time.Now()
	resp, err := http.Get(ts.URL + "/delay")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("expected at least 80ms delay, got %v", elapsed)
	}
}

func TestFaultInjectionNoEffect(t *testing.T) {
	var calls atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		calls.Add(1)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "fault-none",
			Path: "/none",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				FaultInjection: config.FaultInjectionConfig{
					Enabled: true,
					Abort:   config.FaultAbortConfig{Percentage: 0},
					Delay:   config.FaultDelayConfig{Percentage: 0},
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	resp, err := http.Get(ts.URL + "/none")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestFaultInjectionGlobalMerge(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.TrafficShaping = config.TrafficShapingConfig{
		FaultInjection: config.FaultInjectionConfig{
			Enabled: true,
			Abort: config.FaultAbortConfig{
				Percentage: 100,
				StatusCode: 503,
			},
		},
	}
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "fault-global",
			Path: "/global",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	resp, err := http.Get(ts.URL + "/global")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != 503 {
		t.Errorf("expected 503 from global fault injection, got %d", resp.StatusCode)
	}
}

func TestFaultInjectionAdminStats(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`ok`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "fi-stats",
			Path: "/stats",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficShaping: config.TrafficShapingConfig{
				FaultInjection: config.FaultInjectionConfig{
					Enabled: true,
					Abort: config.FaultAbortConfig{
						Percentage: 100,
						StatusCode: 503,
					},
				},
			},
		},
	}

	gw, ts := newTestGateway(t, cfg)

	// Make a request to trigger the fault injector
	resp, _ := http.Get(ts.URL + "/stats")
	if resp != nil {
		resp.Body.Close()
	}

	stats := gw.GetFaultInjectors().Stats()
	if s, ok := stats["fi-stats"]; !ok {
		t.Error("expected fault injection stats for fi-stats route")
	} else {
		if s.TotalRequests != 1 {
			t.Errorf("expected 1 total request, got %d", s.TotalRequests)
		}
		if s.TotalAborted != 1 {
			t.Errorf("expected 1 aborted, got %d", s.TotalAborted)
		}
	}
}

func TestFaultInjectionConfigValidation(t *testing.T) {
	loader := config.NewLoader()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "abort percentage out of range",
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
      fault_injection:
        enabled: true
        abort:
          percentage: 150
          status_code: 503
`,
			wantErr: "abort percentage must be between 0 and 100",
		},
		{
			name: "abort invalid status code",
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
      fault_injection:
        enabled: true
        abort:
          percentage: 50
          status_code: 999
`,
			wantErr: "abort status_code must be between 100 and 599",
		},
		{
			name: "delay without duration",
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
      fault_injection:
        enabled: true
        delay:
          percentage: 50
`,
			wantErr: "delay duration must be > 0 when percentage is set",
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
