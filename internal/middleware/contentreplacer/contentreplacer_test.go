package contentreplacer

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestContentReplacer_BodyReplacement(t *testing.T) {
	cr, err := New(config.ContentReplacerConfig{
		Enabled: true,
		Replacements: []config.ReplacementRule{
			{Pattern: `internal-api\.example\.com`, Replacement: "api.example.com", Scope: "body"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := cr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"url":"https://internal-api.example.com/v1"}`))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	expected := `{"url":"https://api.example.com/v1"}`
	if rr.Body.String() != expected {
		t.Errorf("expected %q, got %q", expected, rr.Body.String())
	}

	stats := cr.Stats()
	if stats["replaced"].(int64) != 1 {
		t.Errorf("expected 1 replaced, got %v", stats["replaced"])
	}
}

func TestContentReplacer_HeaderReplacement(t *testing.T) {
	cr, err := New(config.ContentReplacerConfig{
		Enabled: true,
		Replacements: []config.ReplacementRule{
			{Pattern: `internal`, Replacement: "external", Scope: "header:X-Server"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := cr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Server", "internal-backend-01")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if got := rr.Header().Get("X-Server"); got != "external-backend-01" {
		t.Errorf("expected X-Server=external-backend-01, got %q", got)
	}
}

func TestContentReplacer_SkipsBinaryContent(t *testing.T) {
	cr, err := New(config.ContentReplacerConfig{
		Enabled: true,
		Replacements: []config.ReplacementRule{
			{Pattern: `secret`, Replacement: "REDACTED"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := cr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("this has secret data"))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Body.String() != "this has secret data" {
		t.Error("binary content should not be modified")
	}
}

func TestContentReplacer_CaptureGroups(t *testing.T) {
	cr, err := New(config.ContentReplacerConfig{
		Enabled: true,
		Replacements: []config.ReplacementRule{
			{Pattern: `(\w+)@internal\.com`, Replacement: "${1}@external.com"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := cr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("contact: admin@internal.com"))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	expected := "contact: admin@external.com"
	if rr.Body.String() != expected {
		t.Errorf("expected %q, got %q", expected, rr.Body.String())
	}
}

func TestContentReplacer_InvalidPattern(t *testing.T) {
	_, err := New(config.ContentReplacerConfig{
		Enabled: true,
		Replacements: []config.ReplacementRule{
			{Pattern: `[invalid`, Replacement: "x"},
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestContentReplacer_DefaultScope(t *testing.T) {
	cr, err := New(config.ContentReplacerConfig{
		Enabled: true,
		Replacements: []config.ReplacementRule{
			{Pattern: `foo`, Replacement: "bar"}, // no scope → defaults to "body"
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := cr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("foo"))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Body.String() != "bar" {
		t.Errorf("expected bar, got %q", rr.Body.String())
	}
}

func TestContentReplacerByRoute(t *testing.T) {
	m := NewContentReplacerByRoute()

	err := m.AddRoute("route1", config.ContentReplacerConfig{
		Enabled: true,
		Replacements: []config.ReplacementRule{
			{Pattern: `test`, Replacement: "prod"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if cr := m.Lookup("route1"); cr == nil {
		t.Fatal("expected replacer for route1")
	}
	if cr := m.Lookup("nonexistent"); cr != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("expected [route1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestIsTextContent(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/plain; charset=utf-8", true},
		{"application/json", true},
		{"application/xml", true},
		{"application/xhtml+xml", true},
		{"application/octet-stream", false},
		{"image/png", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isTextContent(tt.ct); got != tt.want {
			t.Errorf("isTextContent(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}
