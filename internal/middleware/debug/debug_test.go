package debug

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Routes: []config.RouteConfig{
			{
				ID:   "route1",
				Path: "/api",
				Methods: []string{"GET", "POST"},
				Backends: []config.BackendConfig{
					{URL: "http://localhost:8080"},
				},
				Auth: config.RouteAuthConfig{Required: true},
				Cache: config.CacheConfig{Enabled: true},
			},
		},
		Listeners: []config.ListenerConfig{
			{Address: ":8080", Protocol: "http"},
		},
	}
}

func TestHandler_RequestEcho(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true}, testConfig())

	req := httptest.NewRequest("POST", "/__debug/request?foo=bar", strings.NewReader("hello"))
	req.Header.Set("X-Custom", "test-value")
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	if result["method"] != "POST" {
		t.Errorf("expected POST, got %v", result["method"])
	}
	if result["body"] != "hello" {
		t.Errorf("expected body=hello, got %v", result["body"])
	}

	headers := result["headers"].(map[string]interface{})
	if headers["X-Custom"] == nil {
		t.Error("expected X-Custom header in response")
	}

	query := result["query"].(map[string]interface{})
	if query["foo"] == nil {
		t.Error("expected foo query param in response")
	}
}

func TestHandler_RequestEchoDefault(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true}, testConfig())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/__debug", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)
	if result["method"] != "GET" {
		t.Errorf("expected GET, got %v", result["method"])
	}
}

func TestHandler_ConfigSummary(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true}, testConfig())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/__debug/config", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)

	routes := result["routes"].([]interface{})
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	route := routes[0].(map[string]interface{})
	if route["id"] != "route1" {
		t.Errorf("expected route1, got %v", route["id"])
	}

	features := route["features"].([]interface{})
	if len(features) != 2 {
		t.Errorf("expected 2 features (auth, cache), got %v", features)
	}

	listeners := result["listeners"].([]interface{})
	if len(listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(listeners))
	}
}

func TestHandler_RuntimeStats(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true}, testConfig())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/__debug/runtime", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(rr.Body.Bytes(), &result)

	if result["goroutines"] == nil {
		t.Error("expected goroutines field")
	}
	if result["cpus"] == nil {
		t.Error("expected cpus field")
	}
	if result["go_version"] == nil {
		t.Error("expected go_version field")
	}
	if result["memory"] == nil {
		t.Error("expected memory field")
	}
	if result["gc"] == nil {
		t.Error("expected gc field")
	}
	if result["uptime_seconds"] == nil {
		t.Error("expected uptime_seconds field")
	}
}

func TestHandler_Matches(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true, Path: "/__debug"}, testConfig())

	tests := []struct {
		path string
		want bool
	}{
		{"/__debug", true},
		{"/__debug/request", true},
		{"/__debug/config", true},
		{"/__debug/runtime", true},
		{"/api/test", false},
		{"/__debugger", false},
	}

	for _, tt := range tests {
		if got := h.Matches(tt.path); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestHandler_CustomPath(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true, Path: "/_internal/debug"}, testConfig())

	if h.Path() != "/_internal/debug" {
		t.Errorf("expected path=/_internal/debug, got %s", h.Path())
	}
	if !h.Matches("/_internal/debug/runtime") {
		t.Error("should match custom path")
	}
	if h.Matches("/__debug") {
		t.Error("should not match default path")
	}
}

func TestHandler_NotFound(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true}, testConfig())

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/__debug/unknown", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandler_Stats(t *testing.T) {
	h := New(config.DebugEndpointConfig{Enabled: true, Path: "/__debug"}, testConfig())
	stats := h.Stats()
	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	if stats["path"] != "/__debug" {
		t.Errorf("expected path=/__debug, got %v", stats["path"])
	}
}
