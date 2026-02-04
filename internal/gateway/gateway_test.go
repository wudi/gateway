package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
)

func TestGatewayNew(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "test",
				Path:     "/test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	if gw.GetRouter() == nil {
		t.Error("Router should not be nil")
	}

	if gw.GetRegistry() == nil {
		t.Error("Registry should not be nil")
	}

	if gw.GetHealthChecker() == nil {
		t.Error("HealthChecker should not be nil")
	}
}

func TestGatewayHandler(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"path":   r.URL.Path,
			"method": r.Method,
		})
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:         "api",
				Path:       "/api",
				PathPrefix: true,
				Backends:   []config.BackendConfig{{URL: backend.URL}},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	// Test route match
	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}

	// Test 404
	resp, err = http.Get(ts.URL + "/unknown")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected 404, got %d", resp.StatusCode)
	}
}

func TestGatewayWithAuth(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Authentication: config.AuthenticationConfig{
			APIKey: config.APIKeyConfig{
				Enabled: true,
				Header:  "X-API-Key",
				Keys: []config.APIKeyEntry{
					{Key: "test-key", ClientID: "test-client"},
				},
			},
		},
		Routes: []config.RouteConfig{
			{
				ID:       "protected",
				Path:     "/protected",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				Auth: config.RouteAuthConfig{
					Required: true,
					Methods:  []string{"api_key"},
				},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	// Without auth
	resp, err := http.Get(ts.URL + "/protected")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 without auth, got %d", resp.StatusCode)
	}

	// With auth
	req, _ := http.NewRequest("GET", ts.URL+"/protected", nil)
	req.Header.Set("X-API-Key", "test-key")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 with auth, got %d", resp.StatusCode)
	}
}

func TestGatewayStats(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "route1",
				Path:     "/route1",
				Backends: []config.BackendConfig{{URL: backend.URL}},
			},
			{
				ID:       "route2",
				Path:     "/route2",
				Backends: []config.BackendConfig{{URL: backend.URL}},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	stats := gw.GetStats()

	if stats.Routes != 2 {
		t.Errorf("Expected 2 routes, got %d", stats.Routes)
	}

	if len(stats.Backends) != 2 {
		t.Errorf("Expected 2 backend entries, got %d", len(stats.Backends))
	}
}

func TestGatewayTransform(t *testing.T) {
	var receivedHeaders http.Header

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "transform",
				Path:     "/transform",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				Transform: config.TransformConfig{
					Request: config.RequestTransform{
						Headers: config.HeaderTransform{
							Add: map[string]string{
								"X-Gateway": "test-gateway",
							},
							Remove: []string{"X-Secret"},
						},
					},
				},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/transform", nil)
	req.Header.Set("X-Secret", "should-be-removed")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	// Check added header
	if receivedHeaders.Get("X-Gateway") != "test-gateway" {
		t.Error("X-Gateway header should be added")
	}

	// Check removed header
	if receivedHeaders.Get("X-Secret") != "" {
		t.Error("X-Secret header should be removed")
	}
}

func TestGatewayCacheHitMiss(t *testing.T) {
	var apiRequestCount atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only count requests to /cached paths, not health checks
		if r.URL.Path != "/health" {
			apiRequestCount.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"data":"from backend"}`))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:         "cached",
				Path:       "/cached",
				PathPrefix: true,
				Backends:   []config.BackendConfig{{URL: backend.URL}},
				Cache: config.CacheConfig{
					Enabled:     true,
					TTL:         5 * time.Second,
					MaxSize:     100,
					MaxBodySize: 1 << 20,
					Methods:     []string{"GET"},
				},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	// First request — cache MISS
	resp, err := http.Get(ts.URL + "/cached/data")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Cache") != "MISS" {
		t.Errorf("Expected X-Cache: MISS, got %s", resp.Header.Get("X-Cache"))
	}
	if string(body) != `{"data":"from backend"}` {
		t.Errorf("Unexpected body: %s", body)
	}
	countAfterFirst := apiRequestCount.Load()

	// Second request — cache HIT
	resp2, err := http.Get(ts.URL + "/cached/data")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected 200, got %d", resp2.StatusCode)
	}
	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("Expected X-Cache: HIT on second request, got %s", resp2.Header.Get("X-Cache"))
	}
	if string(body2) != `{"data":"from backend"}` {
		t.Errorf("Unexpected cached body: %s", body2)
	}

	// Backend should not be called again after the cache hit
	if apiRequestCount.Load() != countAfterFirst {
		t.Errorf("Expected no additional backend calls after cache hit, got %d more",
			apiRequestCount.Load()-countAfterFirst)
	}
}

func TestGatewayCircuitBreaker(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "cb-route",
				Path:     "/cb",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				CircuitBreaker: config.CircuitBreakerConfig{
					Enabled:          true,
					FailureThreshold: 2,
					MaxRequests:      1,
					Timeout:          5 * time.Second,
				},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	// First 2 requests: pass through (backend returns 500, counted as failures)
	for i := 0; i < 2; i++ {
		resp, err := http.Get(ts.URL + "/cb")
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Request %d: expected 500, got %d", i, resp.StatusCode)
		}
	}

	// 3rd request: circuit breaker should be open, return 503
	resp, err := http.Get(ts.URL + "/cb")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 (circuit open), got %d", resp.StatusCode)
	}

	// Verify circuit breaker state via getter
	snapshots := gw.GetCircuitBreakers().Snapshots()
	snap, ok := snapshots["cb-route"]
	if !ok {
		t.Fatal("Expected circuit breaker snapshot for cb-route")
	}
	if snap.State != "open" {
		t.Errorf("Expected circuit state 'open', got '%s'", snap.State)
	}
}

func TestGatewayNoCacheOnNonCachedRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "no-cache",
				Path:     "/no-cache",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				// No cache config
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/no-cache")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	// Should NOT have X-Cache header when caching is not configured
	if resp.Header.Get("X-Cache") != "" {
		t.Errorf("Expected no X-Cache header on non-cached route, got %s", resp.Header.Get("X-Cache"))
	}
}

func TestGatewayNewFeatureGetters(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "test",
				Path:     "/test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
			},
		},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	if gw.GetCircuitBreakers() == nil {
		t.Error("CircuitBreakers should not be nil")
	}
	if gw.GetCaches() == nil {
		t.Error("Caches should not be nil")
	}
	// RetryMetrics should be empty for routes without retry
	metrics := gw.GetRetryMetrics()
	if len(metrics) != 0 {
		t.Errorf("Expected 0 retry metrics, got %d", len(metrics))
	}
}
