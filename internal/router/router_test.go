package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/gateway/internal/config"
)

func TestRouterMatch(t *testing.T) {
	r := New()

	// Add routes
	r.AddRoute(config.RouteConfig{
		ID:         "users",
		Path:       "/api/v1/users",
		PathPrefix: true,
		Backends:   []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	r.AddRoute(config.RouteConfig{
		ID:         "orders",
		Path:       "/api/v1/orders",
		PathPrefix: false,
		Backends:   []config.BackendConfig{{URL: "http://localhost:9002"}},
	})

	r.AddRoute(config.RouteConfig{
		ID:         "user-detail",
		Path:       "/api/v1/users/{id}",
		PathPrefix: false,
		Backends:   []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	tests := []struct {
		name       string
		path       string
		method     string
		wantRoute  string
		wantParams map[string]string
	}{
		{
			name:      "exact match",
			path:      "/api/v1/orders",
			method:    "GET",
			wantRoute: "orders",
		},
		{
			name:      "prefix match with subpath",
			path:      "/api/v1/users/123/profile",
			method:    "GET",
			wantRoute: "users",
		},
		{
			name:      "prefix match root",
			path:      "/api/v1/users",
			method:    "GET",
			wantRoute: "users",
		},
		{
			name:      "param route match",
			path:      "/api/v1/users/123",
			method:    "GET",
			wantRoute: "user-detail",
		},
		{
			name:      "no match",
			path:      "/api/v2/products",
			method:    "GET",
			wantRoute: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			match := r.Match(req)

			if tt.wantRoute == "" {
				if match != nil {
					t.Errorf("expected no match, got route %s", match.Route.ID)
				}
				return
			}

			if match == nil {
				t.Errorf("expected match for route %s, got nil", tt.wantRoute)
				return
			}

			if match.Route.ID != tt.wantRoute {
				t.Errorf("expected route %s, got %s", tt.wantRoute, match.Route.ID)
			}
		})
	}
}

func TestRouterMethodFiltering(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:       "get-only",
		Path:     "/api/readonly",
		Methods:  []string{"GET"},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// GET should match
	req := httptest.NewRequest("GET", "/api/readonly", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("GET request should match")
	}

	// POST should not match
	req = httptest.NewRequest("POST", "/api/readonly", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("POST request should not match")
	}
}

func TestMatcherWithParams(t *testing.T) {
	m := NewMatcher("/users/{id}/posts/{post_id}", false)

	params, ok := m.Match("/users/123/posts/456")
	if !ok {
		t.Error("expected match")
	}

	if params["id"] != "123" {
		t.Errorf("expected id=123, got %s", params["id"])
	}

	if params["post_id"] != "456" {
		t.Errorf("expected post_id=456, got %s", params["post_id"])
	}
}

func TestMatcherPrefix(t *testing.T) {
	m := NewMatcher("/api/v1", true)

	tests := []struct {
		path  string
		match bool
	}{
		{"/api/v1", true},
		{"/api/v1/users", true},
		{"/api/v1/users/123", true},
		{"/api/v2", false},
		{"/api", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, ok := m.Match(tt.path)
			if ok != tt.match {
				t.Errorf("Match(%s) = %v, want %v", tt.path, ok, tt.match)
			}
		})
	}
}

func TestRouteRemove(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:       "test",
		Path:     "/test",
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	req := httptest.NewRequest("GET", "/test", nil)
	if r.Match(req) == nil {
		t.Error("route should exist")
	}

	r.RemoveRoute("test")

	if r.Match(req) != nil {
		t.Error("route should be removed")
	}
}

func BenchmarkRouterMatch(b *testing.B) {
	r := New()

	// Add 100 routes
	for i := 0; i < 100; i++ {
		r.AddRoute(config.RouteConfig{
			ID:         string(rune(i)),
			Path:       "/api/v1/service" + string(rune(i)),
			PathPrefix: true,
			Backends:   []config.BackendConfig{{URL: "http://localhost:9001"}},
		})
	}

	req, _ := http.NewRequest("GET", "/api/v1/service50/users/123", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Match(req)
	}
}
