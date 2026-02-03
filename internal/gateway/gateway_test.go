package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
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
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
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
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
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
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
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
		Server: config.ServerConfig{
			Port:         8080,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		},
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
