package openapi

import (
	"testing"

	"github.com/wudi/runway/config"
)

func TestGenerateRoutes(t *testing.T) {
	boolTrue := true
	specCfg := config.OpenAPISpecConfig{
		ID:   "petstore",
		File: "testdata/petstore.yaml",
		DefaultBackends: []config.BackendConfig{
			{URL: "http://localhost:8080", Weight: 1},
		},
		RoutePrefix: "/api",
		StripPrefix: true,
		Validation: config.OpenAPIValidationOptions{
			Request:  &boolTrue,
			Response: false,
			LogOnly:  false,
		},
	}

	routes, err := GenerateRoutes(specCfg)
	if err != nil {
		t.Fatal(err)
	}

	if len(routes) == 0 {
		t.Fatal("expected generated routes")
	}

	// Find routes by operationId-based ID
	routeMap := make(map[string]config.RouteConfig)
	for _, r := range routes {
		routeMap[r.ID] = r
	}

	// listPets
	if r, ok := routeMap["openapi-listPets"]; ok {
		if r.Path != "/api/pets" {
			t.Errorf("expected path /api/pets, got %s", r.Path)
		}
		if len(r.Methods) != 1 || r.Methods[0] != "GET" {
			t.Errorf("expected [GET], got %v", r.Methods)
		}
		if !r.StripPrefix {
			t.Error("expected strip_prefix to be true")
		}
		if len(r.Backends) != 1 || r.Backends[0].URL != "http://localhost:8080" {
			t.Errorf("expected default backends, got %v", r.Backends)
		}
		if r.OpenAPI.SpecFile != "testdata/petstore.yaml" {
			t.Errorf("expected spec_file, got %s", r.OpenAPI.SpecFile)
		}
		if r.OpenAPI.SpecID != "petstore" {
			t.Errorf("expected spec_id petstore, got %s", r.OpenAPI.SpecID)
		}
		if r.OpenAPI.OperationID != "listPets" {
			t.Errorf("expected operation_id listPets, got %s", r.OpenAPI.OperationID)
		}
		if r.OpenAPI.ValidateRequest == nil || !*r.OpenAPI.ValidateRequest {
			t.Error("expected validate_request true")
		}
	} else {
		t.Error("expected openapi-listPets route")
	}

	// createPet
	if _, ok := routeMap["openapi-createPet"]; !ok {
		t.Error("expected openapi-createPet route")
	}

	// getPet (with path params)
	if r, ok := routeMap["openapi-getPet"]; ok {
		if r.Path != "/api/pets/:petId" {
			t.Errorf("expected path /api/pets/:petId, got %s", r.Path)
		}
		if !r.PathPrefix {
			t.Error("expected path_prefix true for parameterized path")
		}
	} else {
		t.Error("expected openapi-getPet route")
	}
}

func TestGenerateRoutesFallbackID(t *testing.T) {
	// Test with a spec that might have no operationId
	specCfg := config.OpenAPISpecConfig{
		ID:   "test",
		File: "testdata/petstore.yaml",
		DefaultBackends: []config.BackendConfig{
			{URL: "http://localhost:8080"},
		},
	}

	routes, err := GenerateRoutes(specCfg)
	if err != nil {
		t.Fatal(err)
	}

	// All routes in petstore.yaml have operationId, so all should have openapi-{operationId}
	for _, r := range routes {
		if r.ID == "" {
			t.Error("expected non-empty route ID")
		}
	}
}

func TestGenerateRoutesNonexistentSpec(t *testing.T) {
	specCfg := config.OpenAPISpecConfig{
		File: "testdata/nonexistent.yaml",
	}
	_, err := GenerateRoutes(specCfg)
	if err == nil {
		t.Fatal("expected error for nonexistent spec file")
	}
}

func TestConvertOpenAPIPath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/pets", "/pets"},
		{"/pets/{petId}", "/pets/:petId"},
		{"/users/{userId}/posts/{postId}", "/users/:userId/posts/:postId"},
	}

	for _, tc := range tests {
		result := convertOpenAPIPath(tc.input)
		if result != tc.expected {
			t.Errorf("convertOpenAPIPath(%q) = %q, want %q", tc.input, result, tc.expected)
		}
	}
}

func TestGenerateRouteID(t *testing.T) {
	tests := []struct {
		method      string
		path        string
		operationID string
		expected    string
	}{
		{"GET", "/pets", "listPets", "openapi-listPets"},
		{"POST", "/pets", "createPet", "openapi-createPet"},
		{"GET", "/pets/{petId}", "", "openapi-get-pets-petId"},
	}

	for _, tc := range tests {
		result := generateRouteID(tc.method, tc.path, tc.operationID)
		if result != tc.expected {
			t.Errorf("generateRouteID(%q, %q, %q) = %q, want %q", tc.method, tc.path, tc.operationID, result, tc.expected)
		}
	}
}
