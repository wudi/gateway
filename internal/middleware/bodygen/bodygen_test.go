package bodygen

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestBodyGen_BasicTemplate(t *testing.T) {
	cfg := config.BodyGeneratorConfig{
		Enabled:  true,
		Template: `{"method":"{{.Method}}","path":"{{.Path}}"}`,
	}
	bg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBody string
	var gotContentType string
	handler := bg.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("POST", "/api/users", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotBody != `{"method":"POST","path":"/api/users"}` {
		t.Errorf("unexpected body: %s", gotBody)
	}
	if gotContentType != "application/json" {
		t.Errorf("unexpected content type: %s", gotContentType)
	}
	if bg.Generated() != 1 {
		t.Errorf("expected 1 generated, got %d", bg.Generated())
	}
}

func TestBodyGen_QueryParams(t *testing.T) {
	cfg := config.BodyGeneratorConfig{
		Enabled:  true,
		Template: `{"id":"{{first .Query.id}}"}`,
	}
	bg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBody string
	handler := bg.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/api/users?id=123", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotBody != `{"id":"123"}` {
		t.Errorf("unexpected body: %s", gotBody)
	}
}

func TestBodyGen_OriginalBody(t *testing.T) {
	cfg := config.BodyGeneratorConfig{
		Enabled:  true,
		Template: `{"original":"{{.Body}}","host":"{{.Host}}"}`,
	}
	bg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBody string
	handler := bg.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("POST", "/api", strings.NewReader("hello"))
	req.Host = "example.com"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotBody != `{"original":"hello","host":"example.com"}` {
		t.Errorf("unexpected body: %s", gotBody)
	}
}

func TestBodyGen_CustomContentType(t *testing.T) {
	cfg := config.BodyGeneratorConfig{
		Enabled:     true,
		Template:    `<xml>{{.Method}}</xml>`,
		ContentType: "application/xml",
	}
	bg, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotCT string
	handler := bg.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotCT != "application/xml" {
		t.Errorf("expected application/xml, got %s", gotCT)
	}
}

func TestBodyGen_InvalidTemplate(t *testing.T) {
	cfg := config.BodyGeneratorConfig{
		Enabled:  true,
		Template: `{{.Invalid`,
	}
	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

func TestBodyGenByRoute(t *testing.T) {
	m := NewBodyGenByRoute()

	cfg := config.BodyGeneratorConfig{
		Enabled:  true,
		Template: `{"ok":true}`,
	}
	if err := m.AddRoute("route1", cfg); err != nil {
		t.Fatal(err)
	}

	if bg := m.GetGenerator("route1"); bg == nil {
		t.Error("expected generator for route1")
	}
	if bg := m.GetGenerator("route2"); bg != nil {
		t.Error("expected nil for route2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if len(stats) != 1 {
		t.Errorf("unexpected stats length: %d", len(stats))
	}
}
