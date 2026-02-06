// +build integration

package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
)

func TestMirrorConditionsIntegration(t *testing.T) {
	var primaryCalls, mirrorCalls atomic.Int64

	primaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		primaryCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"from": "primary"})
	}))
	defer primaryBackend.Close()

	mirrorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirrorCalls.Add(1)
		w.WriteHeader(200)
	}))
	defer mirrorBackend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "mirror-cond",
			Path: "/api",
			PathPrefix: true,
			Backends: []config.BackendConfig{
				{URL: primaryBackend.URL},
			},
			Mirror: config.MirrorConfig{
				Enabled:    true,
				Backends:   []config.BackendConfig{{URL: mirrorBackend.URL}},
				Percentage: 100,
				Conditions: config.MirrorConditionsConfig{
					Methods: []string{"POST", "PUT"},
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// GET request should NOT be mirrored
	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	if mirrorCalls.Load() != 0 {
		t.Errorf("GET should not be mirrored, got %d mirror calls", mirrorCalls.Load())
	}

	// POST request should be mirrored
	postResp, err := http.Post(ts.URL+"/api/users", "application/json", strings.NewReader(`{"name":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	postResp.Body.Close()
	time.Sleep(500 * time.Millisecond)

	if mirrorCalls.Load() != 1 {
		t.Errorf("POST should be mirrored, expected 1 mirror call, got %d", mirrorCalls.Load())
	}
}

func TestMirrorCompareIntegration(t *testing.T) {
	primaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"result":"primary"}`))
	}))
	defer primaryBackend.Close()

	mirrorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		// Different body to trigger mismatch
		w.Write([]byte(`{"result":"mirror"}`))
	}))
	defer mirrorBackend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "mirror-compare",
			Path: "/compare",
			Backends: []config.BackendConfig{
				{URL: primaryBackend.URL},
			},
			Mirror: config.MirrorConfig{
				Enabled:    true,
				Backends:   []config.BackendConfig{{URL: mirrorBackend.URL}},
				Percentage: 100,
				Compare: config.MirrorCompareConfig{
					Enabled:       true,
					LogMismatches: true,
				},
			},
		},
	}

	gw, ts := newTestGateway(t, cfg)

	// Send request — should return primary response
	resp, err := http.Get(ts.URL + "/compare")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	if result["result"] != "primary" {
		t.Errorf("expected primary response, got %s", result["result"])
	}

	// Wait for mirror
	time.Sleep(500 * time.Millisecond)

	// Check metrics
	stats := gw.GetMirrors().Stats()
	mirrorStats, ok := stats["mirror-compare"]
	if !ok {
		t.Fatal("expected mirror stats for mirror-compare route")
	}
	if mirrorStats.TotalMirrored != 1 {
		t.Errorf("expected TotalMirrored=1, got %d", mirrorStats.TotalMirrored)
	}
	if mirrorStats.TotalCompared != 1 {
		t.Errorf("expected TotalCompared=1, got %d", mirrorStats.TotalCompared)
	}
	// Body differs so should have a mismatch
	if mirrorStats.TotalMismatches != 1 {
		t.Errorf("expected TotalMismatches=1, got %d", mirrorStats.TotalMismatches)
	}
}

func TestMirrorMetricsIntegration(t *testing.T) {
	primaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer primaryBackend.Close()

	mirrorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer mirrorBackend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "mirror-metrics",
			Path: "/metrics-test",
			Backends: []config.BackendConfig{
				{URL: primaryBackend.URL},
			},
			Mirror: config.MirrorConfig{
				Enabled:    true,
				Backends:   []config.BackendConfig{{URL: mirrorBackend.URL}},
				Percentage: 100,
			},
		},
	}

	gw, ts := newTestGateway(t, cfg)

	// Send 5 requests
	for i := 0; i < 5; i++ {
		resp, err := http.Get(ts.URL + "/metrics-test")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	time.Sleep(500 * time.Millisecond)

	stats := gw.GetMirrors().Stats()
	mirrorStats, ok := stats["mirror-metrics"]
	if !ok {
		t.Fatal("expected mirror stats")
	}
	if mirrorStats.TotalMirrored != 5 {
		t.Errorf("expected TotalMirrored=5, got %d", mirrorStats.TotalMirrored)
	}
	if mirrorStats.TotalErrors != 0 {
		t.Errorf("expected TotalErrors=0, got %d", mirrorStats.TotalErrors)
	}
}

func TestMirrorConditionsPathRegexIntegration(t *testing.T) {
	var mirrorCalls atomic.Int64

	primaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.WriteHeader(200)
	}))
	defer primaryBackend.Close()

	mirrorBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mirrorCalls.Add(1)
		w.WriteHeader(200)
	}))
	defer mirrorBackend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:         "mirror-regex",
			Path:       "/api",
			PathPrefix: true,
			Backends: []config.BackendConfig{
				{URL: primaryBackend.URL},
			},
			Mirror: config.MirrorConfig{
				Enabled:    true,
				Backends:   []config.BackendConfig{{URL: mirrorBackend.URL}},
				Percentage: 100,
				Conditions: config.MirrorConditionsConfig{
					PathRegex: `^/api/v2/`,
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// /api/v1 should NOT be mirrored
	resp, _ := http.Get(ts.URL + "/api/v1/users")
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)
	if mirrorCalls.Load() != 0 {
		t.Errorf("v1 should not be mirrored, got %d calls", mirrorCalls.Load())
	}

	// /api/v2 should be mirrored
	resp, _ = http.Get(ts.URL + "/api/v2/users")
	resp.Body.Close()
	time.Sleep(500 * time.Millisecond)
	if mirrorCalls.Load() != 1 {
		t.Errorf("v2 should be mirrored, expected 1 call, got %d", mirrorCalls.Load())
	}
}

func TestMirrorConditionsValidation(t *testing.T) {
	loader := config.NewLoader()

	yaml := `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
    mirror:
      enabled: true
      backends:
        - url: http://localhost:9998
      conditions:
        path_regex: "[invalid"
`
	_, err := loader.Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid path_regex")
	}
	if !strings.Contains(err.Error(), "mirror conditions path_regex is invalid") {
		t.Errorf("unexpected error: %v", err)
	}
}
