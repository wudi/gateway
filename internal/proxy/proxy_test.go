package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/router"
)

func TestProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"path":   r.URL.Path,
			"method": r.Method,
			"host":   r.Host,
		})
	}))
	defer backend.Close()

	proxy := New(Config{})

	route := &router.Route{
		ID:        "test",
		Path:      "/api",
		Transform: config.TransformConfig{},
	}

	backends := []*loadbalancer.Backend{
		{URL: backend.URL, Weight: 1, Healthy: true},
	}
	balancer := loadbalancer.NewRoundRobin(backends)

	handler := proxy.Handler(route, balancer)

	req := httptest.NewRequest("GET", "/api/users", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rr.Code)
	}

	var response map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&response)

	if response["method"] != "GET" {
		t.Errorf("Expected method GET, got %v", response["method"])
	}
}

func TestProxyForwardedHeaders(t *testing.T) {
	var receivedHeaders http.Header

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy := New(Config{})

	route := &router.Route{
		ID:        "test",
		Path:      "/",
		Transform: config.TransformConfig{},
	}

	backends := []*loadbalancer.Backend{
		{URL: backend.URL, Weight: 1, Healthy: true},
	}
	balancer := loadbalancer.NewRoundRobin(backends)

	handler := proxy.Handler(route, balancer)

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.100:12345"
	req.Host = "api.example.com"
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Check X-Forwarded headers
	if receivedHeaders.Get("X-Forwarded-For") == "" {
		t.Error("X-Forwarded-For header should be set")
	}

	if receivedHeaders.Get("X-Forwarded-Proto") != "http" {
		t.Errorf("X-Forwarded-Proto should be http, got %s", receivedHeaders.Get("X-Forwarded-Proto"))
	}

	if receivedHeaders.Get("X-Forwarded-Host") != "api.example.com" {
		t.Errorf("X-Forwarded-Host should be api.example.com, got %s", receivedHeaders.Get("X-Forwarded-Host"))
	}
}

func TestProxyNoHealthyBackends(t *testing.T) {
	proxy := New(Config{})

	route := &router.Route{
		ID:        "test",
		Path:      "/",
		Transform: config.TransformConfig{},
	}

	// All backends unhealthy
	backends := []*loadbalancer.Backend{
		{URL: "http://localhost:9999", Weight: 1, Healthy: false},
	}
	balancer := loadbalancer.NewRoundRobin(backends)

	handler := proxy.Handler(route, balancer)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503, got %d", rr.Code)
	}
}

func TestProxyStripPrefix(t *testing.T) {
	var receivedPath string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy := New(Config{})

	route := &router.Route{
		ID:          "test",
		Path:        "/api/v1",
		PathPrefix:  true,
		StripPrefix: true,
		Transform:   config.TransformConfig{},
	}

	backends := []*loadbalancer.Backend{
		{URL: backend.URL, Weight: 1, Healthy: true},
	}
	balancer := loadbalancer.NewRoundRobin(backends)

	handler := proxy.Handler(route, balancer)

	req := httptest.NewRequest("GET", "/api/v1/users/123", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	// Path should be stripped to /users/123
	if receivedPath != "/users/123" {
		t.Errorf("Expected path /users/123, got %s", receivedPath)
	}
}

func TestRouteProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy := New(Config{})

	route := &router.Route{
		ID:        "test",
		Path:      "/",
		Transform: config.TransformConfig{},
	}

	backends := []*loadbalancer.Backend{
		{URL: backend.URL, Weight: 1, Healthy: true},
	}

	rp := NewRouteProxy(proxy, route, backends)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	rp.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rr.Code)
	}
}

func TestRouteProxyUpdateBackends(t *testing.T) {
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"backend": "1"})
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"backend": "2"})
	}))
	defer backend2.Close()

	proxy := New(Config{})

	route := &router.Route{
		ID:        "test",
		Path:      "/",
		Transform: config.TransformConfig{},
	}

	// Start with backend1
	backends := []*loadbalancer.Backend{
		{URL: backend1.URL, Weight: 1, Healthy: true},
	}

	rp := NewRouteProxy(proxy, route, backends)

	// Update to backend2
	newBackends := []*loadbalancer.Backend{
		{URL: backend2.URL, Weight: 1, Healthy: true},
	}
	rp.UpdateBackends(newBackends)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	rp.ServeHTTP(rr, req)

	var response map[string]string
	json.NewDecoder(rr.Body).Decode(&response)

	if response["backend"] != "2" {
		t.Errorf("Expected backend 2, got %s", response["backend"])
	}
}

func TestSimpleProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	handler, err := SimpleProxy(backend.URL)
	if err != nil {
		t.Fatalf("Failed to create simple proxy: %v", err)
	}

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rr.Code)
	}
}

func TestStripPrefix(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    string
	}{
		{"/api/v1", "/api/v1/users", "/users"},
		{"/api/v1", "/api/v1/users/123", "/users/123"},
		{"/api", "/api/test", "/test"},
		{"/api", "/api", "/"},
		{"", "/test", "/test"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := stripPrefix(tt.pattern, tt.path)
			if got != tt.want {
				t.Errorf("stripPrefix(%q, %q) = %q, want %q", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
