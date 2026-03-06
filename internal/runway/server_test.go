package runway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/ui"
)

func TestNewServerWithListeners(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{
				ID:       "http-test",
				Address:  ":0", // Use random port
				Protocol: config.ProtocolHTTP,
				HTTP: config.HTTPListenerConfig{
					ReadTimeout:  30 * time.Second,
					WriteTimeout: 30 * time.Second,
				},
			},
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
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	// Verify listener manager has listeners
	if server.ListenerManager().Count() != 1 {
		t.Errorf("Expected 1 listener, got %d", server.ListenerManager().Count())
	}
}

func TestNewServerWithDefaultListener(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{
				ID:       "default-http",
				Address:  ":0",
				Protocol: config.ProtocolHTTP,
				HTTP: config.HTTPListenerConfig{
					ReadTimeout:  30 * time.Second,
					WriteTimeout: 30 * time.Second,
				},
			},
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
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	// Should have created default listener
	if server.ListenerManager().Count() != 1 {
		t.Errorf("Expected 1 default listener, got %d", server.ListenerManager().Count())
	}

	// Check default listener ID
	listeners := server.ListenerManager().List()
	if len(listeners) != 1 || listeners[0] != "default-http" {
		t.Errorf("Expected default-http listener, got %v", listeners)
	}
}

func TestServerWithTCPRoutes(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{
				ID:       "tcp-test",
				Address:  ":0",
				Protocol: config.ProtocolTCP,
				TCP: config.TCPListenerConfig{
					SNIRouting:     false,
					ConnectTimeout: 10 * time.Second,
					IdleTimeout:    5 * time.Minute,
				},
			},
		},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		TCPRoutes: []config.TCPRouteConfig{
			{
				ID:       "tcp-route",
				Listener: "tcp-test",
				Backends: []config.BackendConfig{{URL: "tcp://127.0.0.1:3306"}},
			},
		},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server with TCP routes: %v", err)
	}
	defer server.Runway().Close()

	// Verify TCP proxy was initialized
	if server.GetTCPProxy() == nil {
		t.Error("TCP proxy should be initialized")
	}
}

func TestServerWithUDPRoutes(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{
				ID:       "udp-test",
				Address:  ":0",
				Protocol: config.ProtocolUDP,
				UDP: config.UDPListenerConfig{
					SessionTimeout:  30 * time.Second,
					ReadBufferSize:  4096,
					WriteBufferSize: 4096,
				},
			},
		},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		UDPRoutes: []config.UDPRouteConfig{
			{
				ID:       "udp-route",
				Listener: "udp-test",
				Backends: []config.BackendConfig{{URL: "udp://8.8.8.8:53"}},
			},
		},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server with UDP routes: %v", err)
	}
	defer server.Runway().Close()

	// Verify UDP proxy was initialized
	if server.GetUDPProxy() == nil {
		t.Error("UDP proxy should be initialized")
	}
}

func TestAdminListenersEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{
				ID:       "http-1",
				Address:  ":8080",
				Protocol: config.ProtocolHTTP,
			},
			{
				ID:       "http-2",
				Address:  ":8081",
				Protocol: config.ProtocolHTTP,
			},
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
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    8082,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	// Test /listeners endpoint via admin handler
	req := httptest.NewRequest("GET", "/listeners", nil)
	w := httptest.NewRecorder()

	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var listeners []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &listeners); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if len(listeners) != 2 {
		t.Errorf("Expected 2 listeners in response, got %d", len(listeners))
	}
}

func TestAdminStatsWithL4Proxies(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{
				ID:       "tcp-test",
				Address:  ":0",
				Protocol: config.ProtocolTCP,
			},
			{
				ID:       "udp-test",
				Address:  ":0",
				Protocol: config.ProtocolUDP,
			},
		},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		TCPRoutes: []config.TCPRouteConfig{
			{
				ID:       "tcp-1",
				Listener: "tcp-test",
				Backends: []config.BackendConfig{{URL: "tcp://127.0.0.1:3306"}},
			},
			{
				ID:       "tcp-2",
				Listener: "tcp-test",
				Backends: []config.BackendConfig{{URL: "tcp://127.0.0.1:3307"}},
			},
		},
		UDPRoutes: []config.UDPRouteConfig{
			{
				ID:       "udp-1",
				Listener: "udp-test",
				Backends: []config.BackendConfig{{URL: "udp://8.8.8.8:53"}},
			},
		},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    8082,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	// Test /stats endpoint
	req := httptest.NewRequest("GET", "/stats", nil)
	w := httptest.NewRecorder()

	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Check for L4 stats
	if tcpRoutes, ok := stats["tcp_routes"].(float64); !ok || int(tcpRoutes) != 2 {
		t.Errorf("Expected tcp_routes=2, got %v", stats["tcp_routes"])
	}

	if udpRoutes, ok := stats["udp_routes"].(float64); !ok || int(udpRoutes) != 1 {
		t.Errorf("Expected udp_routes=1, got %v", stats["udp_routes"])
	}

	if _, ok := stats["udp_sessions"]; !ok {
		t.Error("Expected udp_sessions in stats")
	}
}

func TestAdminCircuitBreakersEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "cb-test",
				Path:     "/cb-test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				CircuitBreaker: config.CircuitBreakerConfig{
					Enabled:          true,
					FailureThreshold: 5,
					Timeout:          30 * time.Second,
				},
			},
		},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    8082,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	req := httptest.NewRequest("GET", "/circuit-breakers", nil)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if _, ok := result["cb-test"]; !ok {
		t.Error("Expected circuit breaker info for cb-test route")
	}
}

func TestAdminCacheEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "cache-test",
				Path:     "/cache-test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				Cache: config.CacheConfig{
					Enabled: true,
					TTL:     60 * time.Second,
					MaxSize: 100,
				},
			},
		},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    8082,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	req := httptest.NewRequest("GET", "/cache", nil)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if _, ok := result["cache-test"]; !ok {
		t.Error("Expected cache stats for cache-test route")
	}
}

func TestAdminCachePurgeByTags(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Tag", "product listing")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("cached"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{
			{
				ID:       "tag-test",
				Path:     "/tag-test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				Cache: config.CacheConfig{
					Enabled:    true,
					TTL:        60 * time.Second,
					MaxSize:    100,
					TagHeaders: []string{"Cache-Tag"},
					Tags:       []string{"route-tag"},
				},
			},
		},
		Admin: config.AdminConfig{Enabled: true, Port: 8082},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	// Warm cache by making a request
	gwHandler := server.Runway().Handler()
	reqWarm := httptest.NewRequest("GET", "/tag-test/page", nil)
	wWarm := httptest.NewRecorder()
	gwHandler.ServeHTTP(wWarm, reqWarm)

	// Purge by tags
	body := strings.NewReader(`{"route":"tag-test","tags":["route-tag"]}`)
	req := httptest.NewRequest("POST", "/cache/purge", body)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if result["purged"] != true {
		t.Error("Expected purged=true")
	}
}

func TestAdminCachePurgeByPathPattern(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("cached"))
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{
			{
				ID:       "path-test",
				Path:     "/path-test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				Cache: config.CacheConfig{
					Enabled: true,
					TTL:     60 * time.Second,
					MaxSize: 100,
				},
			},
		},
		Admin: config.AdminConfig{Enabled: true, Port: 8082},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	// Purge by path pattern
	body := strings.NewReader(`{"route":"path-test","path_pattern":"/path-test/*"}`)
	req := httptest.NewRequest("POST", "/cache/purge", body)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if result["purged"] != true {
		t.Error("Expected purged=true")
	}
}

func TestAdminCachePurgeNotFound(t *testing.T) {
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
			{
				ID:       "test",
				Path:     "/test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				Cache: config.CacheConfig{
					Enabled: true,
					TTL:     60 * time.Second,
					MaxSize: 100,
				},
			},
		},
		Admin: config.AdminConfig{Enabled: true, Port: 8082},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	// Purge by tags on nonexistent route
	body := strings.NewReader(`{"route":"nonexistent","tags":["tag"]}`)
	req := httptest.NewRequest("POST", "/cache/purge", body)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}

	// Purge by path pattern on nonexistent route
	body = strings.NewReader(`{"route":"nonexistent","path_pattern":"/x/*"}`)
	req = httptest.NewRequest("POST", "/cache/purge", body)
	w = httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminRetriesEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID: "default-http", Address: ":0", Protocol: config.ProtocolHTTP,
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:       "retry-test",
				Path:     "/retry-test",
				Backends: []config.BackendConfig{{URL: backend.URL}},
				RetryPolicy: config.RetryConfig{
					MaxRetries:     3,
					InitialBackoff: 100 * time.Millisecond,
				},
			},
		},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    8082,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	req := httptest.NewRequest("GET", "/retries", nil)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if _, ok := result["retry-test"]; !ok {
		t.Error("Expected retry metrics for retry-test route")
	}
}

func TestDrainEndpoint(t *testing.T) {
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
		Admin: config.AdminConfig{Enabled: true, Port: 8082},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	handler := server.adminHandler()

	// GET /drain — not draining
	req := httptest.NewRequest("GET", "/drain", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	var drainStatus map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &drainStatus)
	if drainStatus["draining"] != false {
		t.Error("Expected draining=false initially")
	}

	// POST /drain — initiate drain
	req = httptest.NewRequest("POST", "/drain", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	var drainResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &drainResp)
	if drainResp["status"] != "draining" {
		t.Errorf("Expected status=draining, got %v", drainResp["status"])
	}

	// GET /drain — now draining
	req = httptest.NewRequest("GET", "/drain", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &drainStatus)
	if drainStatus["draining"] != true {
		t.Error("Expected draining=true after POST /drain")
	}
	if _, ok := drainStatus["drain_start"]; !ok {
		t.Error("Expected drain_start in response")
	}

	// POST /drain again — already draining
	req = httptest.NewRequest("POST", "/drain", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &drainResp)
	if drainResp["status"] != "already_draining" {
		t.Errorf("Expected status=already_draining, got %v", drainResp["status"])
	}
}

func TestReadinessWhenDraining(t *testing.T) {
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
		Admin: config.AdminConfig{Enabled: true, Port: 8082},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	handler := server.adminHandler()

	// Ready before drain
	req := httptest.NewRequest("GET", "/ready", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 before drain, got %d", w.Code)
	}

	// Initiate drain
	server.Drain()

	// Not ready after drain
	req = httptest.NewRequest("GET", "/ready", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 after drain, got %d", w.Code)
	}
	var readyResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &readyResp)
	if readyResp["status"] != "not_ready" {
		t.Errorf("Expected status=not_ready, got %v", readyResp["status"])
	}
}

func TestShutdownWithConfiguredTimeout(t *testing.T) {
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
		Shutdown: config.ShutdownConfig{
			Timeout:    5 * time.Second,
			DrainDelay: 100 * time.Millisecond,
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Shutdown should complete within the configured timeout
	start := time.Now()
	err = server.Shutdown(cfg.Shutdown.Timeout)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	// Should have waited at least drain_delay
	if elapsed < cfg.Shutdown.DrainDelay {
		t.Errorf("Shutdown completed too quickly (%v), expected at least drain_delay (%v)", elapsed, cfg.Shutdown.DrainDelay)
	}

	// Should be marked as draining
	if !server.IsDraining() {
		t.Error("Expected server to be draining after shutdown")
	}
}

func TestDrainInDashboard(t *testing.T) {
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
		Admin: config.AdminConfig{Enabled: true, Port: 8082},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Runway().Close()

	handler := server.adminHandler()

	// Dashboard without draining — no drain key
	req := httptest.NewRequest("GET", "/dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var dashboard map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &dashboard)
	if _, ok := dashboard["drain"]; ok {
		t.Error("Expected no drain key in dashboard when not draining")
	}

	// Start drain
	server.Drain()

	// Dashboard with draining — has drain key
	req = httptest.NewRequest("GET", "/dashboard", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &dashboard)
	drainInfo, ok := dashboard["drain"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected drain key in dashboard when draining")
	}
	if drainInfo["draining"] != true {
		t.Error("Expected draining=true in dashboard drain info")
	}
}

func newTestServerWithAdmin(t *testing.T, uiEnabled bool) *Server {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(backend.Close)

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{
			{ID: "http", Address: ":0", Protocol: config.ProtocolHTTP},
		},
		Registry: config.RegistryConfig{Type: "memory"},
		Routes: []config.RouteConfig{
			{ID: "test", Path: "/test", Backends: []config.BackendConfig{{URL: backend.URL}}},
		},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    8082,
			UI:      config.AdminUIConfig{Enabled: uiEnabled},
		},
	}

	server, err := NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	t.Cleanup(func() { server.Runway().Close() })
	return server
}

func TestUIServingDisabledByDefault(t *testing.T) {
	server := newTestServerWithAdmin(t, false)

	req := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected 404 when UI disabled, got %d", w.Code)
	}
}

func TestUIServingEnabled(t *testing.T) {
	server := newTestServerWithAdmin(t, true)

	req := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Expected text/html Content-Type, got %s", ct)
	}
	if !strings.Contains(w.Body.String(), "<html") {
		t.Error("Expected response body to contain <html")
	}
}

func TestUISPAFallback(t *testing.T) {
	server := newTestServerWithAdmin(t, true)

	// GET /ui/routes should serve index.html (SPA fallback)
	req1 := httptest.NewRequest("GET", "/ui/routes", nil)
	w1 := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Errorf("Expected 200 for /ui/routes, got %d", w1.Code)
	}

	// GET /ui/some/deep/path should also serve index.html
	req2 := httptest.NewRequest("GET", "/ui/some/deep/path", nil)
	w2 := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Expected 200 for /ui/some/deep/path, got %d", w2.Code)
	}

	// Both should serve the same index.html content
	if w1.Body.String() != w2.Body.String() {
		t.Error("Expected identical body for SPA fallback paths")
	}
}

func TestUIStaticAssets(t *testing.T) {
	server := newTestServerWithAdmin(t, true)

	// Find the actual JS asset filename from embedded FS
	entries, err := ui.DistFS.ReadDir("dist/assets")
	if err != nil {
		t.Fatalf("Failed to read embedded assets dir: %v", err)
	}
	var jsFile string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			jsFile = e.Name()
			break
		}
	}
	if jsFile == "" {
		t.Fatal("No JS file found in embedded dist/assets")
	}

	// GET /ui/assets/<hash>.js should serve the JS file
	req := httptest.NewRequest("GET", "/ui/assets/"+jsFile, nil)
	w := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected 200 for /ui/assets/%s, got %d", jsFile, w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("Expected javascript Content-Type, got %s", ct)
	}

	// Non-existent asset falls back to index.html
	req2 := httptest.NewRequest("GET", "/ui/assets/nonexistent.js", nil)
	w2 := httptest.NewRecorder()
	server.adminHandler().ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Errorf("Expected 200 for nonexistent asset (SPA fallback), got %d", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "<html") {
		t.Error("Expected SPA fallback to serve index.html")
	}
}

func TestUIDoesNotConflictWithAPIRoutes(t *testing.T) {
	server := newTestServerWithAdmin(t, true)

	apiPaths := []string{"/dashboard", "/health", "/routes"}
	for _, path := range apiPaths {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		server.adminHandler().ServeHTTP(w, req)

		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Expected application/json for %s, got %s", path, ct)
		}
		if strings.Contains(w.Body.String(), "<html") {
			t.Errorf("API route %s should not return HTML", path)
		}
	}
}
