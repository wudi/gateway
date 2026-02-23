package catalog

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/router"
)

// mockSpecProvider implements SpecProvider for testing.
type mockSpecProvider struct {
	docs map[string]*openapi3.T
}

func (m *mockSpecProvider) GetSpecDocs() map[string]*openapi3.T {
	return m.docs
}

func newTestRoutes() []*router.Route {
	return []*router.Route{
		{
			ID:       "users-api",
			Path:     "/api/users",
			Methods:  map[string]bool{"GET": true, "POST": true},
			Backends: []router.Backend{{URL: "http://users:8080"}},
		},
		{
			ID:       "orders-api",
			Path:     "/api/orders",
			Methods:  map[string]bool{"GET": true},
			Backends: []router.Backend{{URL: "http://orders:8080"}, {URL: "http://orders:8081"}},
		},
	}
}

func newTestRouteConfigs() []config.RouteConfig {
	return []config.RouteConfig{
		{
			ID:   "users-api",
			Path: "/api/users",
			Auth: config.RouteAuthConfig{Required: true},
		},
		{
			ID:   "orders-api",
			Path: "/api/orders",
			Cache: config.CacheConfig{Enabled: true},
		},
	}
}

func TestBuild(t *testing.T) {
	routes := newTestRoutes()
	routeConfigs := newTestRouteConfigs()

	builder := NewBuilder(
		config.CatalogConfig{
			Enabled:     true,
			Title:       "Test API",
			Description: "Test gateway catalog",
		},
		routeConfigs,
		func() []*router.Route { return routes },
		nil,
	)

	catalog := builder.Build()

	if catalog.Title != "Test API" {
		t.Errorf("Title = %q, want %q", catalog.Title, "Test API")
	}
	if catalog.Description != "Test gateway catalog" {
		t.Errorf("Description = %q, want %q", catalog.Description, "Test gateway catalog")
	}
	if catalog.Stats.TotalRoutes != 2 {
		t.Errorf("TotalRoutes = %d, want 2", catalog.Stats.TotalRoutes)
	}
	if catalog.Stats.TotalBackends != 3 {
		t.Errorf("TotalBackends = %d, want 3", catalog.Stats.TotalBackends)
	}
	if len(catalog.Entries) != 2 {
		t.Fatalf("Entries count = %d, want 2", len(catalog.Entries))
	}

	// Check users-api entry
	var usersEntry CatalogEntry
	for _, e := range catalog.Entries {
		if e.ID == "users-api" {
			usersEntry = e
			break
		}
	}
	if usersEntry.Path != "/api/users" {
		t.Errorf("users-api Path = %q, want %q", usersEntry.Path, "/api/users")
	}
	if !usersEntry.Auth {
		t.Error("users-api Auth should be true")
	}
	if usersEntry.Backends != 1 {
		t.Errorf("users-api Backends = %d, want 1", usersEntry.Backends)
	}
}

func TestBuildWithSpecs(t *testing.T) {
	routes := []*router.Route{
		{
			ID:       "users-api",
			Path:     "/api/users",
			Methods:  map[string]bool{"GET": true},
			Backends: []router.Backend{{URL: "http://users:8080"}},
		},
	}

	routeConfigs := []config.RouteConfig{
		{
			ID:   "users-api",
			Path: "/api/users",
			OpenAPI: config.OpenAPIRouteConfig{SpecFile: "specs/users.yaml"},
		},
	}

	specProvider := &mockSpecProvider{
		docs: map[string]*openapi3.T{
			"specs/users.yaml": {
				Info: &openapi3.Info{
					Title:       "Users API",
					Description: "Manages users",
					Version:     "1.0.0",
				},
			},
		},
	}

	builder := NewBuilder(
		config.CatalogConfig{Enabled: true},
		routeConfigs,
		func() []*router.Route { return routes },
		specProvider,
	)

	catalog := builder.Build()

	if catalog.Stats.TotalSpecs != 1 {
		t.Errorf("TotalSpecs = %d, want 1", catalog.Stats.TotalSpecs)
	}
	if len(catalog.Specs) != 1 {
		t.Fatalf("Specs count = %d, want 1", len(catalog.Specs))
	}
	if catalog.Specs[0].Title != "Users API" {
		t.Errorf("Spec title = %q, want %q", catalog.Specs[0].Title, "Users API")
	}

	// Check that entry has spec ID and description from spec
	if catalog.Entries[0].SpecID == "" {
		t.Error("expected entry to have SpecID")
	}
	if catalog.Entries[0].Description != "Manages users" {
		t.Errorf("Description = %q, want %q", catalog.Entries[0].Description, "Manages users")
	}
}

func TestBuildDefaultTitle(t *testing.T) {
	builder := NewBuilder(
		config.CatalogConfig{Enabled: true},
		nil,
		func() []*router.Route { return nil },
		nil,
	)

	catalog := builder.Build()
	if catalog.Title != "API Gateway" {
		t.Errorf("default Title = %q, want %q", catalog.Title, "API Gateway")
	}
}

func TestSanitizeSpecID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"specs/users.yaml", "specs-users-yaml"},
		{"simple", "simple"},
		{"/path/to/spec.json", "-path-to-spec-json"},
	}
	for _, tt := range tests {
		got := sanitizeSpecID(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeSpecID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestHandlerCatalogJSON(t *testing.T) {
	builder := NewBuilder(
		config.CatalogConfig{Enabled: true, Title: "Test"},
		newTestRouteConfigs(),
		func() []*router.Route { return newTestRoutes() },
		nil,
	)

	handler := NewHandler(builder)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var catalog Catalog
	if err := json.NewDecoder(rec.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if catalog.Title != "Test" {
		t.Errorf("catalog title = %q, want %q", catalog.Title, "Test")
	}
}

func TestHandlerUI(t *testing.T) {
	builder := NewBuilder(
		config.CatalogConfig{Enabled: true, Title: "My Gateway"},
		newTestRouteConfigs(),
		func() []*router.Route { return newTestRoutes() },
		nil,
	)

	handler := NewHandler(builder)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/ui", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "My Gateway") {
		t.Error("expected HTML to contain gateway title")
	}
	if !strings.Contains(body, "users-api") {
		t.Error("expected HTML to contain route ID")
	}
}

func TestHandlerSpecsNotFound(t *testing.T) {
	builder := NewBuilder(
		config.CatalogConfig{Enabled: true},
		nil,
		func() []*router.Route { return nil },
		nil,
	)

	handler := NewHandler(builder)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/specs/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestGetSpecDoc(t *testing.T) {
	specProvider := &mockSpecProvider{
		docs: map[string]*openapi3.T{
			"specs/users.yaml": {
				Info: &openapi3.Info{Title: "Users API"},
			},
		},
	}

	builder := NewBuilder(
		config.CatalogConfig{Enabled: true},
		nil,
		func() []*router.Route { return nil },
		specProvider,
	)

	// Valid spec
	doc := builder.GetSpecDoc("specs-users-yaml")
	if doc == nil {
		t.Fatal("expected non-nil spec doc")
	}
	if doc.Info.Title != "Users API" {
		t.Errorf("title = %q, want %q", doc.Info.Title, "Users API")
	}

	// Non-existent spec
	doc = builder.GetSpecDoc("nonexistent")
	if doc != nil {
		t.Error("expected nil for nonexistent spec")
	}
}

func TestBuildTags(t *testing.T) {
	routes := []*router.Route{
		{
			ID:       "grpc-api",
			Path:     "/api.Service/*",
			Methods:  map[string]bool{},
			Backends: []router.Backend{{URL: "http://grpc:50051"}},
		},
	}

	routeConfigs := []config.RouteConfig{
		{
			ID:   "grpc-api",
			Path: "/api.Service/*",
			GRPC: config.GRPCConfig{Enabled: true},
		},
	}

	builder := NewBuilder(
		config.CatalogConfig{Enabled: true},
		routeConfigs,
		func() []*router.Route { return routes },
		nil,
	)

	catalog := builder.Build()
	entry := catalog.Entries[0]
	found := false
	for _, tag := range entry.Tags {
		if tag == "gRPC" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'gRPC' tag, got %v", entry.Tags)
	}
}
