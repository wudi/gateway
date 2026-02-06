package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
)

func TestDiffConfigRouteAddRemove(t *testing.T) {
	old := &config.Config{
		Routes: []config.RouteConfig{
			{ID: "route-a"},
			{ID: "route-b"},
		},
		Listeners: []config.ListenerConfig{
			{ID: "l1"},
		},
	}

	new := &config.Config{
		Routes: []config.RouteConfig{
			{ID: "route-a"},
			{ID: "route-c"},
		},
		Listeners: []config.ListenerConfig{
			{ID: "l1"},
		},
	}

	changes := diffConfig(old, new)

	found := map[string]bool{}
	for _, c := range changes {
		found[c] = true
	}

	if !found["route added: route-c"] {
		t.Error("Expected 'route added: route-c' in changes")
	}
	if !found["route removed: route-b"] {
		t.Error("Expected 'route removed: route-b' in changes")
	}
	if !found["route reloaded: route-a"] {
		t.Error("Expected 'route reloaded: route-a' in changes")
	}
}

func TestDiffConfigListenerChange(t *testing.T) {
	old := &config.Config{
		Listeners: []config.ListenerConfig{{ID: "l1"}},
	}
	new := &config.Config{
		Listeners: []config.ListenerConfig{{ID: "l1"}, {ID: "l2"}},
	}

	changes := diffConfig(old, new)

	found := false
	for _, c := range changes {
		if c == "listeners changed: 1 -> 2" {
			found = true
		}
	}
	if !found {
		t.Errorf("Expected 'listeners changed: 1 -> 2' in changes, got %v", changes)
	}
}

func TestDiffConfigNoChanges(t *testing.T) {
	cfg := &config.Config{
		Routes:    []config.RouteConfig{{ID: "a"}},
		Listeners: []config.ListenerConfig{{ID: "l1"}},
	}

	changes := diffConfig(cfg, cfg)

	// Should have "route reloaded: a" but no added/removed
	for _, c := range changes {
		if c == "route added: a" || c == "route removed: a" {
			t.Errorf("Unexpected change: %s", c)
		}
	}
}

func TestReloadSuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{{
			ID:       "test",
			Path:     "/test",
			Backends: []config.BackendConfig{{URL: backend.URL}},
		}},
		Admin: config.AdminConfig{Enabled: false},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	// Verify initial route
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	gw.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}

	// Reload with additional route
	newCfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{
			{
				ID:       "test",
				Path:     "/test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
			},
			{
				ID:         "new-route",
				Path:       "/new",
				PathPrefix: true,
				Backends:   []config.BackendConfig{{URL: backend.URL}},
			},
		},
		Admin: config.AdminConfig{Enabled: false},
	}

	result := gw.Reload(newCfg)
	if !result.Success {
		t.Fatalf("Reload failed: %s", result.Error)
	}

	if len(result.Changes) == 0 {
		t.Error("Expected changes in reload result")
	}

	// Verify new route works
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/new/hello", nil)
	gw.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected new route 200, got %d", rec.Code)
	}
}

func TestReloadRemovesRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{
			{ID: "keep", Path: "/keep", Backends: []config.BackendConfig{{URL: backend.URL}}},
			{ID: "remove", Path: "/remove", Backends: []config.BackendConfig{{URL: backend.URL}}},
		},
		Admin: config.AdminConfig{Enabled: false},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	// Verify both work
	for _, path := range []string{"/keep", "/remove"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		gw.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200 for %s, got %d", path, rec.Code)
		}
	}

	// Reload without "remove" route
	newCfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{
			{ID: "keep", Path: "/keep", Backends: []config.BackendConfig{{URL: backend.URL}}},
		},
		Admin: config.AdminConfig{Enabled: false},
	}

	result := gw.Reload(newCfg)
	if !result.Success {
		t.Fatalf("Reload failed: %s", result.Error)
	}

	// /keep still works
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/keep", nil)
	gw.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200 for /keep, got %d", rec.Code)
	}

	// /remove should now 404
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/remove", nil)
	gw.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("Expected 404 for /remove, got %d", rec.Code)
	}
}

func TestReloadPreservesSharedState(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{{
			ID:       "test",
			Path:     "/test",
			Backends: []config.BackendConfig{{URL: backend.URL}},
		}},
		Admin: config.AdminConfig{Enabled: false},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	// Save refs to shared infrastructure
	proxyBefore := gw.proxy
	registryBefore := gw.registry
	healthBefore := gw.healthChecker

	// Reload
	result := gw.Reload(cfg)
	if !result.Success {
		t.Fatalf("Reload failed: %s", result.Error)
	}

	// Shared infrastructure should not change
	if gw.proxy != proxyBefore {
		t.Error("Proxy should be preserved across reload")
	}
	if gw.registry != registryBefore {
		t.Error("Registry should be preserved across reload")
	}
	if gw.healthChecker != healthBefore {
		t.Error("Health checker should be preserved across reload")
	}
}

func TestReloadWithLoadBalancerChange(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{{
			ID:           "test",
			Path:         "/test",
			LoadBalancer: "round_robin",
			Backends:     []config.BackendConfig{{URL: backend.URL}},
		}},
		Admin: config.AdminConfig{Enabled: false},
	}

	gw, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	// Reload with least_conn
	newCfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{{
			ID:           "test",
			Path:         "/test",
			LoadBalancer: "least_conn",
			Backends:     []config.BackendConfig{{URL: backend.URL}},
		}},
		Admin: config.AdminConfig{Enabled: false},
	}

	result := gw.Reload(newCfg)
	if !result.Success {
		t.Fatalf("Reload failed: %s", result.Error)
	}

	// Route should still work
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	gw.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rec.Code)
	}

	// Verify config was updated
	info := gw.GetLoadBalancerInfo()
	if routeInfo, ok := info["test"].(map[string]interface{}); ok {
		if routeInfo["algorithm"] != "least_conn" {
			t.Errorf("Expected algorithm least_conn, got %v", routeInfo["algorithm"])
		}
	} else {
		t.Error("Expected test route in load balancer info")
	}
}

func TestAppendReloadHistory(t *testing.T) {
	var history []ReloadResult

	// Add 55 entries
	for i := 0; i < 55; i++ {
		history = appendReloadHistory(history, ReloadResult{
			Success:   true,
			Timestamp: time.Now(),
		})
	}

	// Should be capped at 50
	if len(history) != 50 {
		t.Errorf("Expected 50 entries, got %d", len(history))
	}
}
