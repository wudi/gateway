package sdk

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/gateway/internal/config"
)

type mockSpecProvider struct {
	docs map[string]*openapi3.T
}

func (m *mockSpecProvider) GetSpecDocs() map[string]*openapi3.T {
	return m.docs
}

func buildTestSpec(t *testing.T) *openapi3.T {
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
      summary: List all pets
      parameters:
        - name: limit
          in: query
          schema:
            type: integer
      responses:
        "200":
          description: A list of pets
    post:
      operationId: createPet
      summary: Create a pet
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                name:
                  type: string
      responses:
        "201":
          description: Pet created
  /pets/{petId}:
    get:
      operationId: getPet
      summary: Get a pet by ID
      parameters:
        - name: petId
          in: path
          required: true
          schema:
            type: integer
      responses:
        "200":
          description: A pet
`
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData([]byte(spec))
	if err != nil {
		t.Fatalf("failed to load spec: %v", err)
	}
	return doc
}

func newTestGenerator(t *testing.T) *Generator {
	t.Helper()
	doc := buildTestSpec(t)
	provider := &mockSpecProvider{
		docs: map[string]*openapi3.T{
			"specs/petstore.yaml": doc,
		},
	}
	return NewGenerator(config.SDKConfig{
		Enabled:   true,
		Languages: []string{"go", "python", "typescript"},
		CacheTTL:  time.Hour,
	}, provider)
}

func TestGeneratorList(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/sdk", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["specs"] == nil {
		t.Error("expected specs in response")
	}
	if result["languages"] == nil {
		t.Error("expected languages in response")
	}
}

func TestGeneratorSpecLanguages(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/sdk/specs-petstore-yaml", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["spec_id"] != "specs-petstore-yaml" {
		t.Errorf("unexpected spec_id: %v", result["spec_id"])
	}
}

func TestGeneratorDownloadGo(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/sdk/specs-petstore-yaml/go", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("expected application/zip, got %s", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".zip") {
		t.Errorf("expected zip disposition, got %s", cd)
	}

	// Verify zip contents
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}

	if len(zr.File) == 0 {
		t.Fatal("zip has no files")
	}

	f, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	content, _ := io.ReadAll(f)
	code := string(content)

	// Verify Go code contains expected elements
	if !strings.Contains(code, "package petstore") {
		t.Error("expected package petstore")
	}
	if !strings.Contains(code, "func (c *Client) ListPets") {
		t.Error("expected ListPets method")
	}
	if !strings.Contains(code, "func (c *Client) CreatePet") {
		t.Error("expected CreatePet method")
	}
	if !strings.Contains(code, "func (c *Client) GetPet") {
		t.Error("expected GetPet method")
	}
}

func TestGeneratorDownloadPython(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/sdk/specs-petstore-yaml/python", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	zr, _ := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	f, _ := zr.File[0].Open()
	content, _ := io.ReadAll(f)
	code := string(content)

	if !strings.Contains(code, "class Client:") {
		t.Error("expected Client class")
	}
	if !strings.Contains(code, "def list_pets") {
		t.Error("expected list_pets method")
	}
	if !strings.Contains(code, "def create_pet") {
		t.Error("expected create_pet method")
	}
}

func TestGeneratorDownloadTypeScript(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/sdk/specs-petstore-yaml/typescript", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	zr, _ := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	f, _ := zr.File[0].Open()
	content, _ := io.ReadAll(f)
	code := string(content)

	if !strings.Contains(code, "export class Client") {
		t.Error("expected Client class")
	}
	if !strings.Contains(code, "async listPets") {
		t.Error("expected listPets method")
	}
}

func TestGeneratorCache(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	// First request
	req1 := httptest.NewRequest("GET", "/catalog/sdk/specs-petstore-yaml/go", nil)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)

	// Second request (should be cached)
	req2 := httptest.NewRequest("GET", "/catalog/sdk/specs-petstore-yaml/go", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if !bytes.Equal(rec1.Body.Bytes(), rec2.Body.Bytes()) {
		t.Error("expected identical cached response")
	}
}

func TestGeneratorNotFound(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/sdk/nonexistent/go", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGeneratorUnsupportedLanguage(t *testing.T) {
	gen := newTestGenerator(t)
	mux := http.NewServeMux()
	gen.RegisterRoutes(mux)

	req := httptest.NewRequest("GET", "/catalog/sdk/specs-petstore-yaml/rust", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != 400 {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestWalkSpec(t *testing.T) {
	doc := buildTestSpec(t)
	data := WalkSpec(doc)

	if data.Title != "Petstore" {
		t.Errorf("expected Petstore, got %s", data.Title)
	}
	if data.PackageName != "petstore" {
		t.Errorf("expected petstore, got %s", data.PackageName)
	}
	if len(data.Endpoints) != 3 {
		t.Errorf("expected 3 endpoints, got %d", len(data.Endpoints))
	}
}

func TestFormatFunctions(t *testing.T) {
	if got := FormatGoPath("/pets/{petId}"); got != "/pets/%v" {
		t.Errorf("FormatGoPath: expected /pets/%%v, got %s", got)
	}
	if got := FormatTSPath("/pets/{petId}"); got != "/pets/${petId}" {
		t.Errorf("FormatTSPath: expected /pets/${petId}, got %s", got)
	}
	if got := SnakeCase("listPets"); got != "list_pets" {
		t.Errorf("SnakeCase: expected list_pets, got %s", got)
	}
	if got := FuncName("listPets"); got != "ListPets" {
		t.Errorf("FuncName: expected ListPets, got %s", got)
	}
}
