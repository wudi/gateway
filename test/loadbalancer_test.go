//go:build integration

package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/gateway"
)

func TestLeastConnIntegration(t *testing.T) {
	var backend1Hits, backend2Hits int64

	// Backend1: slow (100ms)
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt64(&backend1Hits, 1)
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"backend":"slow"}`))
	}))
	defer backend1.Close()

	// Backend2: fast (5ms)
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt64(&backend2Hits, 1)
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"backend":"fast"}`))
	}))
	defer backend2.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:           "lc-test",
		Path:         "/api",
		PathPrefix:   true,
		LoadBalancer: "least_conn",
		Backends: []config.BackendConfig{
			{URL: backend1.URL},
			{URL: backend2.URL},
		},
	}}

	_, ts := newTestGateway(t, cfg)

	// Send concurrent requests — least_conn should favour the fast backend
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(ts.URL + "/api/test")
			if err != nil {
				t.Errorf("Request failed: %v", err)
				return
			}
			resp.Body.Close()
		}()
		time.Sleep(10 * time.Millisecond) // stagger
	}
	wg.Wait()

	hits1 := atomic.LoadInt64(&backend1Hits)
	hits2 := atomic.LoadInt64(&backend2Hits)
	t.Logf("Backend1 (slow) hits: %d, Backend2 (fast) hits: %d", hits1, hits2)

	if hits1+hits2 != 20 {
		t.Errorf("Expected 20 total hits, got %d", hits1+hits2)
	}
	if hits2 <= hits1 {
		t.Errorf("Expected fast backend (%d) to get more hits than slow backend (%d)", hits2, hits1)
	}
}

func TestConsistentHashIntegration(t *testing.T) {
	var backend1Hits, backend2Hits int64

	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt64(&backend1Hits, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"backend":"1"}`))
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt64(&backend2Hits, 1)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"backend":"2"}`))
	}))
	defer backend2.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:           "ch-test",
		Path:         "/api",
		PathPrefix:   true,
		LoadBalancer: "consistent_hash",
		ConsistentHash: config.ConsistentHashConfig{
			Key:        "header",
			HeaderName: "X-User-ID",
			Replicas:   150,
		},
		Backends: []config.BackendConfig{
			{URL: backend1.URL},
			{URL: backend2.URL},
		},
	}}

	_, ts := newTestGateway(t, cfg)

	// Same key should always go to the same backend
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/api/test", nil)
		req.Header.Set("X-User-ID", "user-42")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200, got %d", resp.StatusCode)
		}
	}

	hits1 := atomic.LoadInt64(&backend1Hits)
	hits2 := atomic.LoadInt64(&backend2Hits)
	t.Logf("user-42: Backend1=%d, Backend2=%d", hits1, hits2)

	// All 10 should go to one backend
	if hits1 != 10 && hits2 != 10 {
		t.Errorf("Expected all 10 requests to one backend, got %d and %d", hits1, hits2)
	}

	// Different key: also consistent
	atomic.StoreInt64(&backend1Hits, 0)
	atomic.StoreInt64(&backend2Hits, 0)

	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/api/test", nil)
		req.Header.Set("X-User-ID", "user-99")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		resp.Body.Close()
	}

	hits1 = atomic.LoadInt64(&backend1Hits)
	hits2 = atomic.LoadInt64(&backend2Hits)
	t.Logf("user-99: Backend1=%d, Backend2=%d", hits1, hits2)

	if hits1 != 10 && hits2 != 10 {
		t.Errorf("Expected all 10 requests with user-99 to one backend, got %d and %d", hits1, hits2)
	}
}

func TestLeastResponseTimeIntegration(t *testing.T) {
	var backend1Hits, backend2Hits int64

	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt64(&backend1Hits, 1)
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt64(&backend2Hits, 1)
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend2.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{{
		ID:           "lrt-test",
		Path:         "/api",
		PathPrefix:   true,
		LoadBalancer: "least_response_time",
		Backends: []config.BackendConfig{
			{URL: backend1.URL},
			{URL: backend2.URL},
		},
	}}

	_, ts := newTestGateway(t, cfg)

	// Sequential requests — after EWMA converges, fast backend is preferred
	for i := 0; i < 20; i++ {
		resp, err := http.Get(ts.URL + "/api/test")
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()
	}

	hits1 := atomic.LoadInt64(&backend1Hits)
	hits2 := atomic.LoadInt64(&backend2Hits)
	t.Logf("Backend1 (slow) hits: %d, Backend2 (fast) hits: %d", hits1, hits2)

	if hits1+hits2 != 20 {
		t.Errorf("Expected 20 total hits, got %d", hits1+hits2)
	}
	if hits2 <= hits1 {
		t.Errorf("Expected fast backend (%d) to get more hits than slow backend (%d)", hits2, hits1)
	}
}

func TestLoadBalancerAdminEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Listeners = []config.ListenerConfig{{
		ID: "http-main", Address: ":18200", Protocol: config.ProtocolHTTP,
	}}
	cfg.Admin = config.AdminConfig{Enabled: true, Port: 18201}
	cfg.Routes = []config.RouteConfig{
		{
			ID:           "lc-route",
			Path:         "/lc",
			LoadBalancer: "least_conn",
			Backends:     []config.BackendConfig{{URL: backend.URL}},
		},
		{
			ID:           "ch-route",
			Path:         "/ch",
			LoadBalancer: "consistent_hash",
			ConsistentHash: config.ConsistentHashConfig{
				Key:        "header",
				HeaderName: "X-Key",
				Replicas:   100,
			},
			Backends: []config.BackendConfig{{URL: backend.URL}},
		},
		{
			ID:           "lrt-route",
			Path:         "/lrt",
			LoadBalancer: "least_response_time",
			Backends:     []config.BackendConfig{{URL: backend.URL}},
		},
		{
			ID:       "rr-route",
			Path:     "/rr",
			Backends: []config.BackendConfig{{URL: backend.URL}},
		},
	}

	server, err := gateway.NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:18201/load-balancers")
	if err != nil {
		t.Fatalf("Failed to get load-balancers: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	checks := map[string]string{
		"lc-route":  "least_conn",
		"ch-route":  "consistent_hash",
		"lrt-route": "least_response_time",
		"rr-route":  "round_robin",
	}

	for routeID, expectedAlgo := range checks {
		info, ok := result[routeID].(map[string]interface{})
		if !ok {
			t.Errorf("Expected %s in result", routeID)
			continue
		}
		if info["algorithm"] != expectedAlgo {
			t.Errorf("Route %s: expected algorithm %s, got %v", routeID, expectedAlgo, info["algorithm"])
		}
	}

	// Verify consistent_hash config is present
	if chInfo, ok := result["ch-route"].(map[string]interface{}); ok {
		if chInfo["consistent_hash"] == nil {
			t.Error("Expected consistent_hash config for ch-route")
		}
	}
}

func TestReloadAdminEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	cfgYAML := fmt.Sprintf(`
listeners:
  - id: "http-main"
    address: ":18210"
    protocol: "http"
registry:
  type: memory
routes:
  - id: test-route
    path: /test
    path_prefix: true
    backends:
      - url: %s
admin:
  enabled: true
  port: 18211
`, backend.URL)

	tmpFile := t.TempDir() + "/gateway.yaml"
	if err := os.WriteFile(tmpFile, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	loader := config.NewLoader()
	cfg, err := loader.Load(tmpFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	server, err := gateway.NewServer(cfg, tmpFile)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Verify the original route works
	resp, err := http.Get("http://localhost:18210/test/hello")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	// Add a new route
	cfgYAML2 := fmt.Sprintf(`
listeners:
  - id: "http-main"
    address: ":18210"
    protocol: "http"
registry:
  type: memory
routes:
  - id: test-route
    path: /test
    path_prefix: true
    backends:
      - url: %s
  - id: new-route
    path: /new
    path_prefix: true
    backends:
      - url: %s
admin:
  enabled: true
  port: 18211
`, backend.URL, backend.URL)

	if err := os.WriteFile(tmpFile, []byte(cfgYAML2), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	// Trigger reload via admin API
	req, _ := http.NewRequest("POST", "http://localhost:18211/reload", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Reload request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", resp.StatusCode, body)
	}

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if result["success"] != true {
		t.Errorf("Expected reload success, got: %v", result)
	}

	// Verify the new route works
	time.Sleep(50 * time.Millisecond)
	resp2, err := http.Get("http://localhost:18210/new/hello")
	if err != nil {
		t.Fatalf("New route request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected new route to return 200, got %d", resp2.StatusCode)
	}

	// Check reload status
	resp3, err := http.Get("http://localhost:18211/reload/status")
	if err != nil {
		t.Fatalf("Reload status request failed: %v", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()

	var history []interface{}
	json.Unmarshal(body3, &history)
	if len(history) == 0 {
		t.Error("Expected at least one reload result in history")
	}
}

func TestReloadRemovesRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	cfgYAML := fmt.Sprintf(`
listeners:
  - id: "http-main"
    address: ":18220"
    protocol: "http"
registry:
  type: memory
routes:
  - id: route-a
    path: /a
    path_prefix: true
    backends:
      - url: %s
  - id: route-b
    path: /b
    path_prefix: true
    backends:
      - url: %s
admin:
  enabled: true
  port: 18221
`, backend.URL, backend.URL)

	tmpFile := t.TempDir() + "/gateway.yaml"
	if err := os.WriteFile(tmpFile, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	loader := config.NewLoader()
	cfg, err := loader.Load(tmpFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	server, err := gateway.NewServer(cfg, tmpFile)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Verify both routes work
	for _, path := range []string{"/a/test", "/b/test"} {
		resp, err := http.Get("http://localhost:18220" + path)
		if err != nil {
			t.Fatalf("Request to %s failed: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 for %s, got %d", path, resp.StatusCode)
		}
	}

	// Remove route-b
	cfgYAML2 := fmt.Sprintf(`
listeners:
  - id: "http-main"
    address: ":18220"
    protocol: "http"
registry:
  type: memory
routes:
  - id: route-a
    path: /a
    path_prefix: true
    backends:
      - url: %s
admin:
  enabled: true
  port: 18221
`, backend.URL)

	if err := os.WriteFile(tmpFile, []byte(cfgYAML2), 0644); err != nil {
		t.Fatalf("Failed to write updated config: %v", err)
	}

	req, _ := http.NewRequest("POST", "http://localhost:18221/reload", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Reload request failed: %v", err)
	}
	resp.Body.Close()

	time.Sleep(50 * time.Millisecond)

	// route-a still works
	resp, err = http.Get("http://localhost:18220/a/test")
	if err != nil {
		t.Fatalf("Request to /a failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 for /a, got %d", resp.StatusCode)
	}

	// route-b now 404
	resp, err = http.Get("http://localhost:18220/b/test")
	if err != nil {
		t.Fatalf("Request to /b failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected 404 for removed /b route, got %d", resp.StatusCode)
	}
}

func TestReloadInvalidConfig(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfgYAML := fmt.Sprintf(`
listeners:
  - id: "http-main"
    address: ":18230"
    protocol: "http"
registry:
  type: memory
routes:
  - id: test-route
    path: /test
    backends:
      - url: %s
admin:
  enabled: true
  port: 18231
`, backend.URL)

	tmpFile := t.TempDir() + "/gateway.yaml"
	if err := os.WriteFile(tmpFile, []byte(cfgYAML), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	loader := config.NewLoader()
	cfg, err := loader.Load(tmpFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	server, err := gateway.NewServer(cfg, tmpFile)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	// Write invalid config (no listeners)
	if err := os.WriteFile(tmpFile, []byte(`
listeners: []
routes:
  - id: broken
    path: /broken
`), 0644); err != nil {
		t.Fatalf("Failed to write invalid config: %v", err)
	}

	// Reload — should fail
	req, _ := http.NewRequest("POST", "http://localhost:18231/reload", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Reload request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if result["success"] == true {
		t.Error("Expected reload to fail with invalid config")
	}
	if result["error"] == nil || result["error"] == "" {
		t.Error("Expected error message in reload result")
	}

	// Original route should still work
	resp2, err := http.Get("http://localhost:18230/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected original route to still work, got %d", resp2.StatusCode)
	}
}

func TestReloadNoConfigPath(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Listeners = []config.ListenerConfig{{
		ID: "http-main", Address: ":18240", Protocol: config.ProtocolHTTP,
	}}
	cfg.Admin = config.AdminConfig{Enabled: true, Port: 18241}
	cfg.Routes = []config.RouteConfig{{
		ID:       "test-route",
		Path:     "/test",
		Backends: []config.BackendConfig{{URL: backend.URL}},
	}}

	server, err := gateway.NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	req, _ := http.NewRequest("POST", "http://localhost:18241/reload", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Reload request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	var result map[string]interface{}
	json.Unmarshal(body, &result)

	if result["success"] == true {
		t.Error("Expected reload to fail without config path")
	}
}
