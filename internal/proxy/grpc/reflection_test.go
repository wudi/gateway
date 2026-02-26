package grpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestIsReflectionRequest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo", true},
		{"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", true},
		{"/mypackage.MyService/MyMethod", false},
		{"/health", false},
		{"/", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			r := httptest.NewRequest("POST", tt.path, nil)
			got := IsReflectionRequest(r)
			if got != tt.want {
				t.Errorf("IsReflectionRequest(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestNewReflectionProxy(t *testing.T) {
	backends := []string{"http://backend1:50051", "http://backend2:50052"}
	cfg := config.GRPCReflectionConfig{
		Enabled:  true,
		CacheTTL: 0, // should default to 5m
	}

	rp := NewReflectionProxy("test-route", backends, cfg)
	if rp == nil {
		t.Fatal("expected non-nil ReflectionProxy")
	}
	if rp.routeID != "test-route" {
		t.Errorf("routeID = %q, want %q", rp.routeID, "test-route")
	}
	if len(rp.backends) != 2 {
		t.Errorf("backends count = %d, want 2", len(rp.backends))
	}
	if rp.cacheTTL.Minutes() != 5 {
		t.Errorf("cacheTTL = %v, want 5m", rp.cacheTTL)
	}
}

func TestReflectionMiddleware(t *testing.T) {
	backends := []string{"http://backend1:50051"}
	cfg := config.GRPCReflectionConfig{Enabled: true}
	rp := NewReflectionProxy("test-route", backends, cfg)

	// Non-reflection requests should pass through
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := rp.Middleware()(inner)

	// Normal request should pass through
	req := httptest.NewRequest("POST", "/mypackage.MyService/MyMethod", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !innerCalled {
		t.Error("expected inner handler to be called for non-reflection request")
	}
}

func TestReflectionByRoute(t *testing.T) {
	m := NewReflectionByRoute()

	// Initially empty
	if ids := m.RouteIDs(); len(ids) != 0 {
		t.Errorf("expected 0 routes, got %d", len(ids))
	}

	// Add a route
	m.AddRoute("route1", []string{"http://backend1:50051"}, config.GRPCReflectionConfig{Enabled: true})

	if ids := m.RouteIDs(); len(ids) != 1 {
		t.Errorf("expected 1 route, got %d", len(ids))
	}

	// Get proxy
	proxy := m.GetProxy("route1")
	if proxy == nil {
		t.Fatal("expected non-nil proxy")
	}

	// Unknown route
	if p := m.GetProxy("unknown"); p != nil {
		t.Error("expected nil for unknown route")
	}

	// Stats
	stats := m.Stats()
	if len(stats) != 1 {
		t.Errorf("expected 1 stat entry, got %d", len(stats))
	}
}

func TestStripScheme(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"http://localhost:50051", "localhost:50051"},
		{"https://localhost:50051", "localhost:50051"},
		{"localhost:50051", "localhost:50051"},
	}

	for _, tt := range tests {
		got := stripScheme(tt.input)
		if got != tt.want {
			t.Errorf("stripScheme(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestReflectionProxyStats(t *testing.T) {
	rp := NewReflectionProxy("test", []string{"http://b1:50051", "http://b2:50052"}, config.GRPCReflectionConfig{Enabled: true})
	stats := rp.Stats()

	if stats["backends"] != 2 {
		t.Errorf("backends = %v, want 2", stats["backends"])
	}
	if stats["services"] != 0 {
		t.Errorf("services = %v, want 0", stats["services"])
	}
	if stats["requests"] != int64(0) {
		t.Errorf("requests = %v, want 0", stats["requests"])
	}
}
