package specmock

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func buildPetstoreSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec := `
openapi: "3.0.0"
info:
  title: Petstore
  version: "1.0.0"
paths:
  /pets:
    get:
      operationId: listPets
      responses:
        "200":
          description: A list of pets
          content:
            application/json:
              schema:
                type: array
                items:
                  type: object
                  properties:
                    id:
                      type: integer
                      example: 1
                    name:
                      type: string
                      example: "Fido"
              examples:
                dogs:
                  value:
                    - id: 1
                      name: "Rex"
                    - id: 2
                      name: "Buddy"
        "404":
          description: Not found
          content:
            application/json:
              schema:
                type: object
                properties:
                  error:
                    type: string
                    example: "not found"
  /pets/{petId}:
    get:
      operationId: getPet
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: A pet
          content:
            application/json:
              schema:
                type: object
                properties:
                  id:
                    type: integer
                    example: 42
                  name:
                    type: string
                    example: "Fido"
                  species:
                    type: string
                    enum: ["dog", "cat", "bird"]
`
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}
	return doc
}

func TestSpecMockerBasic(t *testing.T) {
	doc := buildPetstoreSpec(t)
	mocker := New(doc, 200, 42, nil)

	req := httptest.NewRequest("GET", "/pets", nil)
	rec := httptest.NewRecorder()
	mocker.Middleware()(nil).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
	if rec.Header().Get("X-Mock-Source") != "openapi-spec" {
		t.Error("expected X-Mock-Source header")
	}

	var body []any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if len(body) == 0 {
		t.Error("expected non-empty array")
	}
}

func TestSpecMockerPreferExample(t *testing.T) {
	doc := buildPetstoreSpec(t)
	mocker := New(doc, 200, 42, nil)

	req := httptest.NewRequest("GET", "/pets", nil)
	req.Header.Set("Prefer", "example=dogs")
	rec := httptest.NewRecorder()
	mocker.Middleware()(nil).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if len(body) != 2 {
		t.Fatalf("expected 2 items, got %d", len(body))
	}
	if body[0]["name"] != "Rex" {
		t.Errorf("expected Rex, got %v", body[0]["name"])
	}
}

func TestSpecMockerPreferStatus(t *testing.T) {
	doc := buildPetstoreSpec(t)
	mocker := New(doc, 200, 42, nil)

	req := httptest.NewRequest("GET", "/pets", nil)
	req.Header.Set("Prefer", "status=404")
	rec := httptest.NewRecorder()
	mocker.Middleware()(nil).ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("expected 404, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if body["error"] != "not found" {
		t.Errorf("expected 'not found', got %v", body["error"])
	}
}

func TestSpecMockerPathParams(t *testing.T) {
	doc := buildPetstoreSpec(t)
	mocker := New(doc, 200, 42, nil)

	req := httptest.NewRequest("GET", "/pets/123", nil)
	rec := httptest.NewRecorder()
	mocker.Middleware()(nil).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	// Schema example should be 42 for id
	if id, ok := body["id"]; ok {
		if idFloat, ok := id.(float64); ok && idFloat != 42 {
			t.Errorf("expected id 42, got %v", id)
		}
	}
}

func TestSpecMockerNoMatch(t *testing.T) {
	doc := buildPetstoreSpec(t)
	mocker := New(doc, 200, 42, nil)

	req := httptest.NewRequest("POST", "/unknown", nil)
	rec := httptest.NewRecorder()
	mocker.Middleware()(nil).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	// Should contain error message
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}
	if body["mock"] != "no matching operation" {
		t.Errorf("expected 'no matching operation', got %v", body["mock"])
	}
}

func TestSpecMockerServedCounter(t *testing.T) {
	doc := buildPetstoreSpec(t)
	mocker := New(doc, 200, 42, nil)

	handler := mocker.Middleware()(nil)
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/pets", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if mocker.Served() != 5 {
		t.Errorf("expected 5 served, got %d", mocker.Served())
	}
}

func TestSpecMockerCustomHeaders(t *testing.T) {
	doc := buildPetstoreSpec(t)
	mocker := New(doc, 200, 42, map[string]string{"X-Custom": "test"})

	req := httptest.NewRequest("GET", "/pets", nil)
	rec := httptest.NewRecorder()
	mocker.Middleware()(nil).ServeHTTP(rec, req)

	if rec.Header().Get("X-Custom") != "test" {
		t.Errorf("expected X-Custom header to be 'test', got '%s'", rec.Header().Get("X-Custom"))
	}
}

func TestPathMatches(t *testing.T) {
	tests := []struct {
		spec, req string
		want      bool
	}{
		{"/pets", "/pets", true},
		{"/pets/{petId}", "/pets/123", true},
		{"/pets/{petId}/toys", "/pets/123/toys", true},
		{"/pets", "/dogs", false},
		{"/pets/{petId}", "/pets/123/extra", false},
		{"/pets/{petId}", "/pets", false},
	}

	for _, tt := range tests {
		got := pathMatches(tt.spec, tt.req)
		if got != tt.want {
			t.Errorf("pathMatches(%q, %q) = %v, want %v", tt.spec, tt.req, got, tt.want)
		}
	}
}

func TestParsePrefer(t *testing.T) {
	if s := parsePreferStatus("status=404"); s != 404 {
		t.Errorf("expected 404, got %d", s)
	}
	if s := parsePreferStatus("example=dogs, status=201"); s != 201 {
		t.Errorf("expected 201, got %d", s)
	}
	if s := parsePreferStatus("example=dogs"); s != 0 {
		t.Errorf("expected 0, got %d", s)
	}
	if e := parsePreferExample("example=dogs"); e != "dogs" {
		t.Errorf("expected dogs, got %s", e)
	}
	if e := parsePreferExample("status=200, example=cats"); e != "cats" {
		t.Errorf("expected cats, got %s", e)
	}
}

func TestDeterministicSeed(t *testing.T) {
	doc := buildPetstoreSpec(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	// Two mockers with same seed should produce same output
	m1 := New(doc, 200, 42, nil)
	m2 := New(doc, 200, 42, nil)

	req1 := httptest.NewRequest("GET", "/pets/1", nil)
	rec1 := httptest.NewRecorder()
	m1.Middleware()(handler).ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest("GET", "/pets/1", nil)
	rec2 := httptest.NewRecorder()
	m2.Middleware()(handler).ServeHTTP(rec2, req2)

	if rec1.Body.String() != rec2.Body.String() {
		t.Errorf("deterministic seed produced different output:\n  %s\n  %s", rec1.Body.String(), rec2.Body.String())
	}
}
