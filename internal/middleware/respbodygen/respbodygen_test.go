package respbodygen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestRespBodyGen_BasicTemplate(t *testing.T) {
	cfg := config.ResponseBodyGeneratorConfig{
		Enabled:  true,
		Template: `{"status": "{{.StatusCode}}", "original": {{json .Parsed}}}`,
	}

	rbg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		json.NewEncoder(w).Encode(map[string]string{"key": "value"})
	})

	handler := rbg.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := w.Body.String()
	if body == "" {
		t.Fatal("empty response body")
	}

	// Verify generated count
	if rbg.Generated() != 1 {
		t.Errorf("expected generated=1, got %d", rbg.Generated())
	}
}

func TestRespBodyGen_CustomContentType(t *testing.T) {
	cfg := config.ResponseBodyGeneratorConfig{
		Enabled:     true,
		Template:    `<xml>{{.Body}}</xml>`,
		ContentType: "application/xml",
	}

	rbg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	})

	handler := rbg.Middleware()(inner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if ct := w.Header().Get("Content-Type"); ct != "application/xml" {
		t.Errorf("expected application/xml, got %s", ct)
	}
	if w.Body.String() != "<xml>hello</xml>" {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}

func TestRespBodyGen_ParsedJSON(t *testing.T) {
	cfg := config.ResponseBodyGeneratorConfig{
		Enabled:  true,
		Template: `{"name": "{{index (index .Parsed "items") 0}}"}`,
	}

	rbg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"items":["first","second"]}`))
	})

	handler := rbg.Middleware()(inner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON output: %s", w.Body.String())
	}
	if result["name"] != "first" {
		t.Errorf("expected name=first, got %v", result["name"])
	}
}

func TestRespBodyGen_TemplateError(t *testing.T) {
	cfg := config.ResponseBodyGeneratorConfig{
		Enabled:  true,
		Template: `{{.NonExistent.Deep.Field}}`,
	}

	rbg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	handler := rbg.Middleware()(inner)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	// Should fall back to original response
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRespBodyGen_InvalidTemplate(t *testing.T) {
	cfg := config.ResponseBodyGeneratorConfig{
		Enabled:  true,
		Template: `{{.Invalid`,
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestRespBodyGenByRoute(t *testing.T) {
	m := NewRespBodyGenByRoute()

	cfg := config.ResponseBodyGeneratorConfig{
		Enabled:  true,
		Template: `{"wrapped": true}`,
	}

	if err := m.AddRoute("r1", cfg); err != nil {
		t.Fatal(err)
	}

	if m.GetGenerator("r1") == nil {
		t.Error("expected generator for r1")
	}
	if m.GetGenerator("r2") != nil {
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
