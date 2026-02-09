package errorpages

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/variables"
)

func TestNew_NilWhenNotActive(t *testing.T) {
	ep, err := New(config.ErrorPagesConfig{}, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if ep != nil {
		t.Fatal("expected nil when neither global nor per-route is active")
	}
}

func TestNew_GlobalOnly(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"404": {JSON: `{"error":"not found","code":{{.StatusCode}}}`},
		},
	}
	ep, err := New(global, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if ep == nil {
		t.Fatal("expected non-nil")
	}
	if !ep.ShouldIntercept(404) {
		t.Error("expected ShouldIntercept(404) to be true")
	}
	if ep.ShouldIntercept(200) {
		t.Error("expected ShouldIntercept(200) to be false")
	}
}

func TestNew_PerRouteOverridesGlobal(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"404": {JSON: `{"source":"global"}`},
		},
	}
	perRoute := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"404": {JSON: `{"source":"route"}`},
		},
	}
	ep, err := New(global, perRoute)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("Accept", "application/json")
	body, ct := ep.Render(404, r, nil)
	if ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}
	if body != `{"source":"route"}` {
		t.Errorf("expected route override, got %s", body)
	}
}

func TestFallbackChain(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"404":     {JSON: `{"page":"exact-404"}`},
			"4xx":     {JSON: `{"page":"class-4xx"}`},
			"default": {JSON: `{"page":"default"}`},
		},
	}
	ep, err := New(global, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "application/json")

	// Exact match
	body, _ := ep.Render(404, r, nil)
	if body != `{"page":"exact-404"}` {
		t.Errorf("expected exact match, got %s", body)
	}

	// Class fallback
	body, _ = ep.Render(403, r, nil)
	if body != `{"page":"class-4xx"}` {
		t.Errorf("expected class fallback, got %s", body)
	}

	// Default fallback
	body, _ = ep.Render(500, r, nil)
	if body != `{"page":"default"}` {
		t.Errorf("expected default fallback, got %s", body)
	}
}

func TestContentNegotiation(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"default": {
				HTML: `<h1>{{.StatusCode}} {{.StatusText}}</h1>`,
				JSON: `{"code":{{.StatusCode}}}`,
				XML:  `<error><code>{{.StatusCode}}</code></error>`,
			},
		},
	}
	ep, err := New(global, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		accept      string
		wantCT      string
		wantContain string
	}{
		{"text/html", "text/html; charset=utf-8", "<h1>500"},
		{"application/json", "application/json", `"code":500`},
		{"application/xml", "application/xml; charset=utf-8", "<code>500</code>"},
		{"text/xml", "application/xml; charset=utf-8", "<code>500</code>"},
		{"*/*", "application/json", `"code":500`}, // default to JSON
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept", tt.accept)
		body, ct := ep.Render(500, r, nil)
		if ct != tt.wantCT {
			t.Errorf("accept=%q: got ct=%q, want %q", tt.accept, ct, tt.wantCT)
		}
		if !contains(body, tt.wantContain) {
			t.Errorf("accept=%q: body=%q doesn't contain %q", tt.accept, body, tt.wantContain)
		}
	}
}

func TestTemplateData(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"default": {JSON: `{"code":{{.StatusCode}},"path":"{{.RequestPath}}","route":"{{.RouteID}}"}`},
		},
	}
	ep, err := New(global, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/api/users", nil)
	r.Header.Set("Accept", "application/json")
	varCtx := variables.NewContext(r)
	varCtx.RouteID = "my-route"
	varCtx.RequestID = "req-123"

	body, _ := ep.Render(502, r, varCtx)
	if !contains(body, `"code":502`) {
		t.Errorf("missing code: %s", body)
	}
	if !contains(body, `"path":"/api/users"`) {
		t.Errorf("missing path: %s", body)
	}
	if !contains(body, `"route":"my-route"`) {
		t.Errorf("missing route: %s", body)
	}
}

func TestFileTemplate(t *testing.T) {
	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "error.html")
	err := os.WriteFile(htmlPath, []byte(`<h1>Error {{.StatusCode}}</h1>`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"500": {HTMLFile: htmlPath},
		},
	}
	ep, err := New(global, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "text/html")
	body, ct := ep.Render(500, r, nil)
	if ct != "text/html; charset=utf-8" {
		t.Errorf("got ct=%s", ct)
	}
	if body != "<h1>Error 500</h1>" {
		t.Errorf("got body=%s", body)
	}
}

func TestInvalidTemplate(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"500": {JSON: `{{.Invalid`},
		},
	}
	_, err := New(global, config.ErrorPagesConfig{})
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestInvalidKey(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"abc": {JSON: `{}`},
		},
	}
	_, err := New(global, config.ErrorPagesConfig{})
	if err == nil {
		t.Error("expected error for invalid key")
	}
}

func TestDefaultBody(t *testing.T) {
	data := TemplateData{StatusCode: 404, StatusText: "Not Found"}

	html := defaultBody("html", data)
	if !contains(html, "404") || !contains(html, "Not Found") {
		t.Errorf("html default: %s", html)
	}

	jsonBody := defaultBody("json", data)
	if !contains(jsonBody, "404") {
		t.Errorf("json default: %s", jsonBody)
	}

	xmlBody := defaultBody("xml", data)
	if !contains(xmlBody, "404") {
		t.Errorf("xml default: %s", xmlBody)
	}
}

func TestShouldIntercept_NoMatch(t *testing.T) {
	// Only 404 configured, 500 should not intercept
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"404": {JSON: `{}`},
		},
	}
	ep, err := New(global, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if ep.ShouldIntercept(500) {
		t.Error("expected ShouldIntercept(500) to be false when only 404 configured")
	}
}

func TestManager(t *testing.T) {
	m := NewErrorPagesByRoute()

	globalCfg := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"default": {JSON: `{"error":"default"}`},
		},
	}

	err := m.AddRoute("route1", globalCfg, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if ep := m.GetErrorPages("route1"); ep == nil {
		t.Error("expected non-nil for route1")
	}
	if ep := m.GetErrorPages("route2"); ep != nil {
		t.Error("expected nil for route2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestMetrics(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"default": {JSON: `{"error":"oops"}`},
		},
	}
	ep, err := New(global, config.ErrorPagesConfig{})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "application/json")
	ep.Render(500, r, nil)
	ep.Render(404, r, nil)

	snap := ep.Metrics()
	if snap.TotalRendered != 2 {
		t.Errorf("expected 2 renders, got %d", snap.TotalRendered)
	}
}

func TestMissingFile(t *testing.T) {
	global := config.ErrorPagesConfig{
		Enabled: true,
		Pages: map[string]config.ErrorPageEntry{
			"500": {HTMLFile: "/nonexistent/file.html"},
		},
	}
	_, err := New(global, config.ErrorPagesConfig{})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
