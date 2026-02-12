package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/wudi/gateway/internal/config"
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
			name:       "param route match",
			path:       "/api/v1/users/123",
			method:     "GET",
			wantRoute:  "user-detail",
			wantParams: map[string]string{"id": "123"},
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

			for k, v := range tt.wantParams {
				if match.PathParams[k] != v {
					t.Errorf("expected param %s=%s, got %s", k, v, match.PathParams[k])
				}
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

func TestPathParamNormalization(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:       "param-route",
		Path:     "/users/{id}/posts/{post_id}",
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	req := httptest.NewRequest("GET", "/users/123/posts/456", nil)
	match := r.Match(req)
	if match == nil {
		t.Fatal("expected match")
	}

	if match.PathParams["id"] != "123" {
		t.Errorf("expected id=123, got %s", match.PathParams["id"])
	}

	if match.PathParams["post_id"] != "456" {
		t.Errorf("expected post_id=456, got %s", match.PathParams["post_id"])
	}
}

func TestPrefixMatch(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:         "prefix",
		Path:       "/api/v1",
		PathPrefix: true,
		Backends:   []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

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
			req := httptest.NewRequest("GET", tt.path, nil)
			m := r.Match(req)
			got := m != nil
			if got != tt.match {
				t.Errorf("Match(%s) = %v, want %v", tt.path, got, tt.match)
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

func TestDomainMatchExact(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "api-route",
		Path: "/data",
		Match: config.MatchConfig{
			Domains: []string{"api.example.com"},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Matching domain
	req := httptest.NewRequest("GET", "http://api.example.com/data", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for exact domain")
	}

	// Non-matching domain
	req = httptest.NewRequest("GET", "http://other.example.com/data", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match wrong domain")
	}
}

func TestDomainMatchWildcard(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "wildcard-route",
		Path: "/data",
		Match: config.MatchConfig{
			Domains: []string{"*.example.com"},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Matching wildcard
	req := httptest.NewRequest("GET", "http://api.example.com/data", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for wildcard domain")
	}

	req = httptest.NewRequest("GET", "http://web.example.com/data", nil)
	match = r.Match(req)
	if match == nil {
		t.Error("expected match for wildcard domain (web)")
	}

	// Non-matching
	req = httptest.NewRequest("GET", "http://api.other.com/data", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match different base domain")
	}
}

func TestHeaderMatchExact(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "v2-route",
		Path: "/api",
		Match: config.MatchConfig{
			Headers: []config.HeaderMatchConfig{
				{Name: "X-Version", Value: "v2"},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// With matching header
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Version", "v2")
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for exact header value")
	}

	// Without header
	req = httptest.NewRequest("GET", "/api", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match without header")
	}

	// Wrong value
	req = httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Version", "v1")
	match = r.Match(req)
	if match != nil {
		t.Error("should not match wrong header value")
	}
}

func TestHeaderMatchPresent(t *testing.T) {
	r := New()

	boolTrue := true
	r.AddRoute(config.RouteConfig{
		ID:   "debug-route",
		Path: "/api",
		Match: config.MatchConfig{
			Headers: []config.HeaderMatchConfig{
				{Name: "X-Debug", Present: &boolTrue},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// With header present
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Debug", "anything")
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for present header")
	}

	// Without header
	req = httptest.NewRequest("GET", "/api", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match without header")
	}
}

func TestHeaderMatchRegex(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "mobile-route",
		Path: "/api",
		Match: config.MatchConfig{
			Headers: []config.HeaderMatchConfig{
				{Name: "X-Client", Regex: "^mobile-.*"},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Matching regex
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Client", "mobile-ios")
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for regex header")
	}

	// Non-matching
	req = httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Client", "desktop")
	match = r.Match(req)
	if match != nil {
		t.Error("should not match non-matching regex")
	}
}

func TestQueryMatchExact(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "json-route",
		Path: "/api",
		Match: config.MatchConfig{
			Query: []config.QueryMatchConfig{
				{Name: "format", Value: "json"},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Matching query
	req := httptest.NewRequest("GET", "/api?format=json", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for exact query value")
	}

	// Non-matching
	req = httptest.NewRequest("GET", "/api?format=xml", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match wrong query value")
	}

	// Missing query
	req = httptest.NewRequest("GET", "/api", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match missing query")
	}
}

func TestQueryMatchPresent(t *testing.T) {
	r := New()

	boolTrue := true
	r.AddRoute(config.RouteConfig{
		ID:   "verbose-route",
		Path: "/api",
		Match: config.MatchConfig{
			Query: []config.QueryMatchConfig{
				{Name: "verbose", Present: &boolTrue},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// With query param present
	req := httptest.NewRequest("GET", "/api?verbose=true", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for present query")
	}

	// Without query param
	req = httptest.NewRequest("GET", "/api", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match missing query param")
	}
}

func TestQueryMatchRegex(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "fields-route",
		Path: "/api",
		Match: config.MatchConfig{
			Query: []config.QueryMatchConfig{
				{Name: "fields", Regex: "^[a-z,]+$"},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Matching regex
	req := httptest.NewRequest("GET", "/api?fields=name,email", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for regex query")
	}

	// Non-matching
	req = httptest.NewRequest("GET", "/api?fields=Name,123", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match non-matching regex query")
	}
}

func TestCookieMatchExact(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "beta-route",
		Path: "/app",
		Match: config.MatchConfig{
			Cookies: []config.CookieMatchConfig{
				{Name: "beta", Value: "true"},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// With matching cookie
	req := httptest.NewRequest("GET", "/app", nil)
	req.AddCookie(&http.Cookie{Name: "beta", Value: "true"})
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for exact cookie value")
	}

	// Without cookie
	req = httptest.NewRequest("GET", "/app", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match without cookie")
	}

	// Wrong value
	req = httptest.NewRequest("GET", "/app", nil)
	req.AddCookie(&http.Cookie{Name: "beta", Value: "false"})
	match = r.Match(req)
	if match != nil {
		t.Error("should not match wrong cookie value")
	}
}

func TestCookieMatchPresent(t *testing.T) {
	r := New()

	boolTrue := true
	r.AddRoute(config.RouteConfig{
		ID:   "tracked-route",
		Path: "/app",
		Match: config.MatchConfig{
			Cookies: []config.CookieMatchConfig{
				{Name: "session", Present: &boolTrue},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// With cookie present
	req := httptest.NewRequest("GET", "/app", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "abc123"})
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for present cookie")
	}

	// Without cookie
	req = httptest.NewRequest("GET", "/app", nil)
	match = r.Match(req)
	if match != nil {
		t.Error("should not match without cookie")
	}
}

func TestCookieMatchPresentFalse(t *testing.T) {
	r := New()

	boolFalse := false
	r.AddRoute(config.RouteConfig{
		ID:   "no-session-route",
		Path: "/app",
		Match: config.MatchConfig{
			Cookies: []config.CookieMatchConfig{
				{Name: "session", Present: &boolFalse},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Without cookie — should match (present: false)
	req := httptest.NewRequest("GET", "/app", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("expected match when cookie is absent and present: false")
	}

	// With cookie — should NOT match
	req = httptest.NewRequest("GET", "/app", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: "abc"})
	match = r.Match(req)
	if match != nil {
		t.Error("should not match when cookie exists and present: false")
	}
}

func TestCookieMatchRegex(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "ab-route",
		Path: "/app",
		Match: config.MatchConfig{
			Cookies: []config.CookieMatchConfig{
				{Name: "variant", Regex: "^(group-a|group-b)$"},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Matching regex
	req := httptest.NewRequest("GET", "/app", nil)
	req.AddCookie(&http.Cookie{Name: "variant", Value: "group-a"})
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for regex cookie")
	}

	// Non-matching
	req = httptest.NewRequest("GET", "/app", nil)
	req.AddCookie(&http.Cookie{Name: "variant", Value: "group-c"})
	match = r.Match(req)
	if match != nil {
		t.Error("should not match non-matching regex cookie")
	}
}

func TestMultiRouteSpecificity(t *testing.T) {
	r := New()

	// Less specific: no match criteria
	r.AddRoute(config.RouteConfig{
		ID:       "fallback",
		Path:     "/api",
		Backends: []config.BackendConfig{{URL: "http://fallback:9001"}},
	})

	// More specific: exact domain
	r.AddRoute(config.RouteConfig{
		ID:   "domain-specific",
		Path: "/api",
		Match: config.MatchConfig{
			Domains: []string{"api.example.com"},
		},
		Backends: []config.BackendConfig{{URL: "http://specific:9001"}},
	})

	// Request with matching domain should hit specific route
	req := httptest.NewRequest("GET", "http://api.example.com/api", nil)
	match := r.Match(req)
	if match == nil {
		t.Fatal("expected match")
	}
	if match.Route.ID != "domain-specific" {
		t.Errorf("expected domain-specific, got %s", match.Route.ID)
	}

	// Request without matching domain should hit fallback
	req = httptest.NewRequest("GET", "http://other.com/api", nil)
	match = r.Match(req)
	if match == nil {
		t.Fatal("expected match")
	}
	if match.Route.ID != "fallback" {
		t.Errorf("expected fallback, got %s", match.Route.ID)
	}
}

func TestSpecificityExactDomainBeatsWildcard(t *testing.T) {
	r := New()

	// Wildcard domain
	r.AddRoute(config.RouteConfig{
		ID:   "wildcard",
		Path: "/api",
		Match: config.MatchConfig{
			Domains: []string{"*.example.com"},
		},
		Backends: []config.BackendConfig{{URL: "http://wildcard:9001"}},
	})

	// Exact domain (more specific)
	r.AddRoute(config.RouteConfig{
		ID:   "exact",
		Path: "/api",
		Match: config.MatchConfig{
			Domains: []string{"api.example.com"},
		},
		Backends: []config.BackendConfig{{URL: "http://exact:9001"}},
	})

	req := httptest.NewRequest("GET", "http://api.example.com/api", nil)
	match := r.Match(req)
	if match == nil {
		t.Fatal("expected match")
	}
	if match.Route.ID != "exact" {
		t.Errorf("expected exact, got %s", match.Route.ID)
	}

	// Different subdomain should hit wildcard
	req = httptest.NewRequest("GET", "http://web.example.com/api", nil)
	match = r.Match(req)
	if match == nil {
		t.Fatal("expected match")
	}
	if match.Route.ID != "wildcard" {
		t.Errorf("expected wildcard, got %s", match.Route.ID)
	}
}

func TestSpecificityHeadersAddScore(t *testing.T) {
	r := New()

	// No match criteria (score 0)
	r.AddRoute(config.RouteConfig{
		ID:       "default",
		Path:     "/api",
		Backends: []config.BackendConfig{{URL: "http://default:9001"}},
	})

	// With header matcher (score 10)
	r.AddRoute(config.RouteConfig{
		ID:   "versioned",
		Path: "/api",
		Match: config.MatchConfig{
			Headers: []config.HeaderMatchConfig{
				{Name: "X-Version", Value: "v2"},
			},
		},
		Backends: []config.BackendConfig{{URL: "http://versioned:9001"}},
	})

	// Request with header should match versioned
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("X-Version", "v2")
	match := r.Match(req)
	if match == nil {
		t.Fatal("expected match")
	}
	if match.Route.ID != "versioned" {
		t.Errorf("expected versioned, got %s", match.Route.ID)
	}

	// Request without header should match default
	req = httptest.NewRequest("GET", "/api", nil)
	match = r.Match(req)
	if match == nil {
		t.Fatal("expected match")
	}
	if match.Route.ID != "default" {
		t.Errorf("expected default, got %s", match.Route.ID)
	}
}

func TestGetRoutes(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:       "a",
		Path:     "/a",
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})
	r.AddRoute(config.RouteConfig{
		ID:       "b",
		Path:     "/b",
		Backends: []config.BackendConfig{{URL: "http://localhost:9002"}},
	})

	routes := r.GetRoutes()
	if len(routes) != 2 {
		t.Errorf("expected 2 routes, got %d", len(routes))
	}
}

func TestUpdateBackends(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:       "test",
		Path:     "/test",
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	ok := r.UpdateBackends("test", []Backend{
		{URL: "http://new:9001", Weight: 1},
		{URL: "http://new:9002", Weight: 1},
	})
	if !ok {
		t.Error("UpdateBackends should return true")
	}

	route := r.GetRoute("test")
	if len(route.Backends) != 2 {
		t.Errorf("expected 2 backends, got %d", len(route.Backends))
	}
}

func TestReplaceParams(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/users/{id}", "/users/:id"},
		{"/users/{id}/posts/{post_id}", "/users/:id/posts/:post_id"},
		{"/static/path", "/static/path"},
		{"/{a}/{b}/{c}", "/:a/:b/:c"},
	}

	for _, tt := range tests {
		got := replaceParams(tt.input)
		if got != tt.expected {
			t.Errorf("replaceParams(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		path     string
		expected int
	}{
		{"/", 0},
		{"/users", 1},
		{"/users/123", 2},
		{"/api/v1/users", 3},
	}

	for _, tt := range tests {
		got := splitPath(tt.path)
		if len(got) != tt.expected {
			t.Errorf("splitPath(%q) returned %d segments, want %d", tt.path, len(got), tt.expected)
		}
	}
}

func TestMatchConfigDomainWithPort(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:   "domain-port",
		Path: "/api",
		Match: config.MatchConfig{
			Domains: []string{"api.example.com"},
		},
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	// Host header with port should still match
	req := httptest.NewRequest("GET", "/api", nil)
	req.Host = "api.example.com:8080"
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for domain with port")
	}
}

func TestRootPath(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:       "root",
		Path:     "/",
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	req := httptest.NewRequest("GET", "/", nil)
	match := r.Match(req)
	if match == nil {
		t.Error("expected match for root path")
	}
}

func TestRootPrefixMatchesAll(t *testing.T) {
	r := New()

	r.AddRoute(config.RouteConfig{
		ID:         "root-prefix",
		Path:       "/",
		PathPrefix: true,
		Backends:   []config.BackendConfig{{URL: "http://localhost:9001"}},
	})

	paths := []string{"/", "/foo", "/foo/bar"}
	for _, p := range paths {
		req := httptest.NewRequest("GET", p, nil)
		match := r.Match(req)
		if match == nil {
			t.Errorf("expected match for path %s with root prefix", p)
		}
	}
}

func TestRewritePathPrefix(t *testing.T) {
	route := &Route{
		Path:       "/api/v1",
		PathPrefix: true,
		Rewrite: config.RewriteConfig{
			Prefix: "/v2",
		},
	}

	tests := []struct {
		input string
		want  string
	}{
		{"/api/v1/users", "/v2/users"},
		{"/api/v1/users/123", "/v2/users/123"},
		{"/api/v1", "/v2/"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := route.RewritePath(tt.input)
			if got != tt.want {
				t.Errorf("RewritePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRewritePathRegex(t *testing.T) {
	route := &Route{
		Path: "/users",
		Rewrite: config.RewriteConfig{
			Regex:       `^/users/(\d+)/posts$`,
			Replacement: "/posts?uid=$1",
		},
	}
	// Simulate the compiled regex (normally done in AddRoute)
	route.rewriteRegex = regexp.MustCompile(route.Rewrite.Regex)

	tests := []struct {
		input string
		want  string
	}{
		{"/users/42/posts", "/posts?uid=42"},
		{"/users/999/posts", "/posts?uid=999"},
		// non-matching path passes through unchanged
		{"/users/42/comments", "/users/42/comments"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := route.RewritePath(tt.input)
			if got != tt.want {
				t.Errorf("RewritePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRewritePathNoRewrite(t *testing.T) {
	route := &Route{
		Path: "/api",
	}

	input := "/api/test"
	got := route.RewritePath(input)
	if got != input {
		t.Errorf("RewritePath(%q) = %q, want passthrough %q", input, got, input)
	}
}

func TestRewritePathRegexMultiCapture(t *testing.T) {
	route := &Route{
		Path: "/",
		Rewrite: config.RewriteConfig{
			Regex:       `^/api/(\w+)/(\d+)$`,
			Replacement: "/v2/$1/item/$2",
		},
	}
	route.rewriteRegex = regexp.MustCompile(route.Rewrite.Regex)

	got := route.RewritePath("/api/products/42")
	want := "/v2/products/item/42"
	if got != want {
		t.Errorf("RewritePath = %q, want %q", got, want)
	}
}

func TestHasRewriteRegex(t *testing.T) {
	route := &Route{Path: "/api"}
	if route.HasRewriteRegex() {
		t.Error("expected HasRewriteRegex() = false for route without regex")
	}

	route.rewriteRegex = regexp.MustCompile(`^/test$`)
	if !route.HasRewriteRegex() {
		t.Error("expected HasRewriteRegex() = true for route with regex")
	}
}

func TestAddRouteCompilesRewriteRegex(t *testing.T) {
	r := New()

	err := r.AddRoute(config.RouteConfig{
		ID:       "rewrite-regex",
		Path:     "/api",
		Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
		Rewrite: config.RewriteConfig{
			Regex:       `^/api/(\d+)$`,
			Replacement: "/v2/$1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	route := r.GetRoute("rewrite-regex")
	if route == nil {
		t.Fatal("route not found")
	}
	if !route.HasRewriteRegex() {
		t.Error("expected rewrite regex to be compiled in AddRoute")
	}
}

func BenchmarkRouterMatch(b *testing.B) {
	r := New()

	// Add 100 routes
	for i := 0; i < 100; i++ {
		r.AddRoute(config.RouteConfig{
			ID:         fmt.Sprintf("route-%d", i),
			Path:       fmt.Sprintf("/api/v1/service%d", i),
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

func BenchmarkRouterMatchWithMatchers(b *testing.B) {
	r := New()

	// Add 100 routes with various matchers
	for i := 0; i < 100; i++ {
		r.AddRoute(config.RouteConfig{
			ID:   fmt.Sprintf("route-%d", i),
			Path: "/api",
			Match: config.MatchConfig{
				Domains: []string{fmt.Sprintf("svc%d.example.com", i)},
			},
			Backends: []config.BackendConfig{{URL: "http://localhost:9001"}},
		})
	}

	req, _ := http.NewRequest("GET", "http://svc50.example.com/api", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Match(req)
	}
}
