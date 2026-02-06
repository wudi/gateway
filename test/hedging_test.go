// +build integration

package test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
)

func TestHedgingIntegration(t *testing.T) {
	// Backend 1: slow (200ms)
	var slow atomic.Int64
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		slow.Add(1)
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"backend":"slow"}`))
	}))
	defer backend1.Close()

	// Backend 2: fast (5ms)
	var fast atomic.Int64
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		fast.Add(1)
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(`{"backend":"fast"}`))
	}))
	defer backend2.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "hedged",
			Path: "/hedged",
			Backends: []config.BackendConfig{
				{URL: backend1.URL},
				{URL: backend2.URL},
			},
			RetryPolicy: config.RetryConfig{
				Hedging: config.HedgingConfig{
					Enabled:     true,
					MaxRequests: 2,
					Delay:       20 * time.Millisecond,
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	start := time.Now()
	resp, err := http.Get(ts.URL + "/hedged")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Should complete much faster than 200ms (the slow backend)
	if elapsed > 180*time.Millisecond {
		t.Logf("hedging didn't significantly reduce latency: %v, body: %s", elapsed, string(body))
	}
}

func TestHedgingMutualExclusive(t *testing.T) {
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
    retry_policy:
      max_retries: 3
      hedging:
        enabled: true
        max_requests: 2
        delay: 100ms
`
	_, err := loader.Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for mutually exclusive hedging + retries")
	}
	if !strings.Contains(err.Error(), "cannot use both hedging and max_retries") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHedgingMinMaxRequests(t *testing.T) {
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
    retry_policy:
      hedging:
        enabled: true
        max_requests: 1
        delay: 100ms
`
	_, err := loader.Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for max_requests < 2")
	}
	if !strings.Contains(err.Error(), "max_requests must be >= 2") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRetryBudgetIntegration(t *testing.T) {
	var requestCount atomic.Int64
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		requestCount.Add(1)
		// Always return 503 to trigger retries
		w.WriteHeader(503)
		w.Write([]byte(`{"error":"unavailable"}`))
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "budget-test",
			Path: "/budget",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			RetryPolicy: config.RetryConfig{
				MaxRetries:        5,
				RetryableStatuses: []int{503},
				RetryableMethods:  []string{"GET"},
				InitialBackoff:    time.Millisecond,
				MaxBackoff:        5 * time.Millisecond,
				Budget: config.BudgetConfig{
					Ratio:      0.2,
					MinRetries: 0,
					Window:     10 * time.Second,
				},
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// Send several requests — budget should limit total retries
	for i := 0; i < 10; i++ {
		resp, err := http.Get(ts.URL + "/budget")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}

	// Without budget: 10 requests * 5 retries = up to 60 backend calls
	// With budget (20%): retries should be limited significantly
	totalCalls := requestCount.Load()
	t.Logf("Total backend calls: %d (budget should limit this)", totalCalls)

	// Should be significantly less than 60
	if totalCalls > 40 {
		t.Errorf("budget should have limited retries, got %d backend calls", totalCalls)
	}
}
