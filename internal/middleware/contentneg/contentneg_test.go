package contentneg

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestNegotiator_JSONPassthrough(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "xml", "yaml"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"key": "value"})
	})

	handler := n.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestNegotiator_XMLConversion(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "xml"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"alice","age":30}`))
	})

	handler := n.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/xml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/xml") {
		t.Errorf("expected application/xml, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "<response>") {
		t.Errorf("expected XML wrapper, got: %s", body)
	}
	if !strings.Contains(body, "<name>alice</name>") {
		t.Errorf("expected name element, got: %s", body)
	}
}

func TestNegotiator_YAMLConversion(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "yaml"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"alice"}`))
	})

	handler := n.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/yaml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/yaml") {
		t.Errorf("expected application/yaml, got %s", ct)
	}

	body := w.Body.String()
	if !strings.Contains(body, "name:") || !strings.Contains(body, "alice") {
		t.Errorf("expected YAML with name: alice, got: %s", body)
	}
}

func TestNegotiator_NotAcceptable(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("should not reach"))
	})

	handler := n.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/xml")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNotAcceptable {
		t.Errorf("expected 406, got %d", w.Code)
	}
}

func TestNegotiator_QualityFactor(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "xml"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// xml preferred over json
	format := n.negotiate("application/json;q=0.5, application/xml;q=0.9")
	if format != "xml" {
		t.Errorf("expected xml, got %s", format)
	}
}

func TestNegotiator_WildcardDefault(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "xml"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	format := n.negotiate("*/*")
	if format != "json" {
		t.Errorf("expected json (default), got %s", format)
	}
}

func TestNegotiator_EmptyAccept(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "xml"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	format := n.negotiate("")
	if format != "json" {
		t.Errorf("expected json (default), got %s", format)
	}
}

func TestNew_ValidationErrors(t *testing.T) {
	// Unsupported format
	_, err := New(config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "protobuf"},
		Default:   "json",
	})
	if err == nil {
		t.Error("expected error for unsupported format")
	}

	// Default not in supported
	_, err = New(config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json"},
		Default:   "xml",
	})
	if err == nil {
		t.Error("expected error for default not in supported")
	}
}

func TestNegotiatorByRoute(t *testing.T) {
	m := NewNegotiatorByRoute()

	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "xml"},
		Default:   "json",
	}

	if err := m.AddRoute("r1", cfg); err != nil {
		t.Fatal(err)
	}

	if m.GetNegotiator("r1") == nil {
		t.Error("expected negotiator for r1")
	}
	if m.GetNegotiator("r2") != nil {
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

func TestJsonToXML_Array(t *testing.T) {
	data := []byte(`{"items":[1,2,3]}`)
	result, err := jsonToXML(data)
	if err != nil {
		t.Fatal(err)
	}

	s := string(result)
	if !strings.Contains(s, "<items>") {
		t.Errorf("expected items element, got: %s", s)
	}
	if !strings.Contains(s, "<item>") {
		t.Errorf("expected item elements, got: %s", s)
	}
}

func TestJsonToXML_Nested(t *testing.T) {
	data := []byte(`{"user":{"name":"alice","active":true}}`)
	result, err := jsonToXML(data)
	if err != nil {
		t.Fatal(err)
	}

	s := string(result)
	if !strings.Contains(s, "<user>") {
		t.Errorf("expected user element, got: %s", s)
	}
	if !strings.Contains(s, "<name>alice</name>") {
		t.Errorf("expected name element, got: %s", s)
	}
	if !strings.Contains(s, "<active>true</active>") {
		t.Errorf("expected active element, got: %s", s)
	}
}

func TestNegotiator_Stats(t *testing.T) {
	cfg := config.ContentNegotiationConfig{
		Enabled:   true,
		Supported: []string{"json", "xml"},
		Default:   "json",
	}

	n, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})

	handler := n.Middleware()(inner)

	// JSON request
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// XML request
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/xml")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	stats := n.Stats()
	if stats["json_count"] != int64(1) {
		t.Errorf("expected json_count=1, got %v", stats["json_count"])
	}
	if stats["xml_count"] != int64(1) {
		t.Errorf("expected xml_count=1, got %v", stats["xml_count"])
	}
}
