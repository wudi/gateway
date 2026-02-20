package fieldreplacer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func jsonHandler(data map[string]interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(data)
	}
}

func TestFieldReplacer_Upper(t *testing.T) {
	cfg := config.FieldReplacerConfig{
		Enabled: true,
		Operations: []config.FieldReplacerOperation{
			{Field: "name", Type: "upper"},
		},
	}

	fr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := jsonHandler(map[string]interface{}{"name": "alice", "age": 30})
	handler := fr.Middleware()(inner)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %s", w.Body.String())
	}

	if result["name"] != "ALICE" {
		t.Errorf("expected ALICE, got %v", result["name"])
	}

	// Numeric field should be untouched.
	if result["age"] != float64(30) {
		t.Errorf("expected age=30, got %v", result["age"])
	}

	if fr.Processed() != 1 {
		t.Errorf("expected processed=1, got %d", fr.Processed())
	}
}

func TestFieldReplacer_Regexp(t *testing.T) {
	cfg := config.FieldReplacerConfig{
		Enabled: true,
		Operations: []config.FieldReplacerOperation{
			{Field: "email", Type: "regexp", Find: `@(.+)$`, Replace: "@redacted.com"},
		},
	}

	fr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := jsonHandler(map[string]interface{}{"email": "user@example.com"})
	handler := fr.Middleware()(inner)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %s", w.Body.String())
	}

	if result["email"] != "user@redacted.com" {
		t.Errorf("expected user@redacted.com, got %v", result["email"])
	}
}

func TestFieldReplacer_Trim(t *testing.T) {
	cfg := config.FieldReplacerConfig{
		Enabled: true,
		Operations: []config.FieldReplacerOperation{
			{Field: "value", Type: "trim"},
		},
	}

	fr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := jsonHandler(map[string]interface{}{"value": "  hello  "})
	handler := fr.Middleware()(inner)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %s", w.Body.String())
	}

	if result["value"] != "hello" {
		t.Errorf("expected 'hello', got %q", result["value"])
	}
}

func TestFieldReplacer_TrimCustomChars(t *testing.T) {
	cfg := config.FieldReplacerConfig{
		Enabled: true,
		Operations: []config.FieldReplacerOperation{
			{Field: "path", Type: "trim", Find: "/"},
		},
	}

	fr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := jsonHandler(map[string]interface{}{"path": "/api/v1/"})
	handler := fr.Middleware()(inner)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %s", w.Body.String())
	}

	if result["path"] != "api/v1" {
		t.Errorf("expected 'api/v1', got %q", result["path"])
	}
}

func TestFieldReplacer_InvalidRegexp(t *testing.T) {
	cfg := config.FieldReplacerConfig{
		Enabled: true,
		Operations: []config.FieldReplacerOperation{
			{Field: "x", Type: "regexp", Find: "[invalid"},
		},
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid regexp")
	}
}

func TestFieldReplacer_NonJSON(t *testing.T) {
	cfg := config.FieldReplacerConfig{
		Enabled: true,
		Operations: []config.FieldReplacerOperation{
			{Field: "name", Type: "upper"},
		},
	}

	fr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		w.Write([]byte("not json"))
	})

	handler := fr.Middleware()(inner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Body.String() != "not json" {
		t.Errorf("expected pass-through for non-JSON, got %q", w.Body.String())
	}

	if fr.Processed() != 0 {
		t.Errorf("expected processed=0 for non-JSON, got %d", fr.Processed())
	}
}

func TestFieldReplacerByRoute(t *testing.T) {
	m := NewFieldReplacerByRoute()

	cfg := config.FieldReplacerConfig{
		Enabled: true,
		Operations: []config.FieldReplacerOperation{
			{Field: "name", Type: "lower"},
		},
	}

	if err := m.AddRoute("r1", cfg); err != nil {
		t.Fatal(err)
	}

	if m.GetReplacer("r1") == nil {
		t.Error("expected replacer for r1")
	}
	if m.GetReplacer("r2") != nil {
		t.Error("expected nil for r2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 {
		t.Errorf("expected 1 route, got %d", len(ids))
	}

	stats := m.Stats()
	if stats["r1"] == nil {
		t.Error("expected stats for r1")
	}
}
