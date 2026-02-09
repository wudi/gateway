package versioning

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func testConfig(source string) config.VersioningConfig {
	return config.VersioningConfig{
		Enabled:        true,
		Source:         source,
		DefaultVersion: "1",
		PathPrefix:     "/v",
		StripPrefix:    true,
		HeaderName:     "X-API-Version",
		QueryParam:     "version",
		Versions: map[string]config.VersionBackendConfig{
			"1": {
				Backends:   []config.BackendConfig{{URL: "http://v1:8080"}},
				Deprecated: true,
				Sunset:     "2025-12-31",
			},
			"2": {
				Backends: []config.BackendConfig{{URL: "http://v2:8080"}},
			},
		},
	}
}

func TestDetectVersion_Path(t *testing.T) {
	v, err := New(testConfig("path"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		path    string
		version string
	}{
		{"/v2/users", "2"},
		{"/v1/orders", "1"},
		{"/v3/items", "1"}, // unknown version falls back to default
		{"/users", "1"},    // no prefix falls back to default
		{"/v", "1"},        // empty version segment falls back to default
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", tt.path, nil)
		got := v.DetectVersion(r)
		if got != tt.version {
			t.Errorf("DetectVersion(%s) = %q, want %q", tt.path, got, tt.version)
		}
	}
}

func TestDetectVersion_Header(t *testing.T) {
	v, err := New(testConfig("header"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// With header
	r := httptest.NewRequest("GET", "/users", nil)
	r.Header.Set("X-API-Version", "2")
	if got := v.DetectVersion(r); got != "2" {
		t.Errorf("DetectVersion (header=2) = %q, want %q", got, "2")
	}

	// Without header -> default
	r2 := httptest.NewRequest("GET", "/users", nil)
	if got := v.DetectVersion(r2); got != "1" {
		t.Errorf("DetectVersion (no header) = %q, want %q", got, "1")
	}
}

func TestDetectVersion_Accept(t *testing.T) {
	v, err := New(testConfig("accept"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest("GET", "/users", nil)
	r.Header.Set("Accept", "application/vnd.myapi.v2+json")
	if got := v.DetectVersion(r); got != "2" {
		t.Errorf("DetectVersion (accept) = %q, want %q", got, "2")
	}

	r2 := httptest.NewRequest("GET", "/users", nil)
	r2.Header.Set("Accept", "application/json")
	if got := v.DetectVersion(r2); got != "1" {
		t.Errorf("DetectVersion (no vnd) = %q, want %q", got, "1")
	}
}

func TestDetectVersion_Query(t *testing.T) {
	v, err := New(testConfig("query"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest("GET", "/users?version=2", nil)
	if got := v.DetectVersion(r); got != "2" {
		t.Errorf("DetectVersion (query=2) = %q, want %q", got, "2")
	}

	r2 := httptest.NewRequest("GET", "/users", nil)
	if got := v.DetectVersion(r2); got != "1" {
		t.Errorf("DetectVersion (no query) = %q, want %q", got, "1")
	}
}

func TestStripVersionPrefix(t *testing.T) {
	v, err := New(testConfig("path"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tests := []struct {
		path     string
		version  string
		expected string
	}{
		{"/v2/users/123", "2", "/users/123"},
		{"/v1/", "1", "/"},
		{"/v1", "1", "/"},
		{"/users", "1", "/users"}, // no prefix -> no change
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", tt.path, nil)
		v.StripVersionPrefix(r, tt.version)
		if r.URL.Path != tt.expected {
			t.Errorf("StripVersionPrefix(%s, %s) = %q, want %q", tt.path, tt.version, r.URL.Path, tt.expected)
		}
	}
}

func TestStripVersionPrefix_NotPath(t *testing.T) {
	cfg := testConfig("header")
	cfg.StripPrefix = true
	v, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// StripPrefix should be a no-op for non-path sources
	r := httptest.NewRequest("GET", "/v2/users", nil)
	v.StripVersionPrefix(r, "2")
	if r.URL.Path != "/v2/users" {
		t.Errorf("expected no strip for header source, got %q", r.URL.Path)
	}
}

func TestInjectDeprecationHeaders(t *testing.T) {
	v, err := New(testConfig("path"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Version 1 is deprecated
	w := httptest.NewRecorder()
	v.InjectDeprecationHeaders(w, "1")
	if got := w.Header().Get("Deprecation"); got != "true" {
		t.Errorf("Deprecation header = %q, want %q", got, "true")
	}
	if got := w.Header().Get("Sunset"); got != "2025-12-31" {
		t.Errorf("Sunset header = %q, want %q", got, "2025-12-31")
	}

	// Version 2 is not deprecated
	w2 := httptest.NewRecorder()
	v.InjectDeprecationHeaders(w2, "2")
	if got := w2.Header().Get("Deprecation"); got != "" {
		t.Errorf("Deprecation header for v2 = %q, want empty", got)
	}
	if got := w2.Header().Get("Sunset"); got != "" {
		t.Errorf("Sunset header for v2 = %q, want empty", got)
	}
}

func TestSnapshot(t *testing.T) {
	v, err := New(testConfig("path"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Make some requests
	r := httptest.NewRequest("GET", "/v2/users", nil)
	v.DetectVersion(r)
	v.DetectVersion(r)

	r2 := httptest.NewRequest("GET", "/v1/users", nil)
	v.DetectVersion(r2)

	snap := v.Snapshot()
	if snap.Source != "path" {
		t.Errorf("Source = %q, want %q", snap.Source, "path")
	}
	if snap.DefaultVersion != "1" {
		t.Errorf("DefaultVersion = %q, want %q", snap.DefaultVersion, "1")
	}
	if snap.Versions["2"].Requests != 2 {
		t.Errorf("v2 requests = %d, want %d", snap.Versions["2"].Requests, 2)
	}
	if snap.Versions["1"].Requests != 1 {
		t.Errorf("v1 requests = %d, want %d", snap.Versions["1"].Requests, 1)
	}
	if !snap.Versions["1"].Deprecated {
		t.Error("v1 should be deprecated")
	}
}

func TestManager(t *testing.T) {
	m := NewVersioningByRoute()

	err := m.AddRoute("api", testConfig("path"))
	if err != nil {
		t.Fatalf("AddRoute: %v", err)
	}

	if v := m.GetVersioner("api"); v == nil {
		t.Fatal("expected versioner for route 'api'")
	}
	if v := m.GetVersioner("other"); v != nil {
		t.Fatal("expected nil for unknown route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "api" {
		t.Errorf("RouteIDs = %v, want [api]", ids)
	}

	stats := m.Stats()
	if _, ok := stats["api"]; !ok {
		t.Error("expected stats for 'api'")
	}
}

func TestDefaults(t *testing.T) {
	cfg := config.VersioningConfig{
		Enabled:        true,
		Source:         "header",
		DefaultVersion: "1",
		Versions: map[string]config.VersionBackendConfig{
			"1": {Backends: []config.BackendConfig{{URL: "http://v1:8080"}}},
		},
	}

	v, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if v.headerName != "X-API-Version" {
		t.Errorf("headerName = %q, want %q", v.headerName, "X-API-Version")
	}
	if v.queryParam != "version" {
		t.Errorf("queryParam = %q, want %q", v.queryParam, "version")
	}
	if v.pathPrefix != "/v" {
		t.Errorf("pathPrefix = %q, want %q", v.pathPrefix, "/v")
	}
}

func TestDetectVersion_UnknownFallsBack(t *testing.T) {
	v, err := New(testConfig("header"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	r := httptest.NewRequest("GET", "/users", nil)
	r.Header.Set("X-API-Version", "99")
	got := v.DetectVersion(r)
	if got != "1" {
		t.Errorf("unknown version should fall back to default, got %q", got)
	}

	snap := v.Snapshot()
	if snap.UnknownCount != 1 {
		t.Errorf("unknown count = %d, want 1", snap.UnknownCount)
	}
}

// Ensure Versioner does NOT call http.ResponseWriter.WriteHeader prematurely.
func TestInjectHeaders_NoWriteHeader(t *testing.T) {
	v, err := New(testConfig("path"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	w := httptest.NewRecorder()
	v.InjectDeprecationHeaders(w, "1")

	// Headers should be set but WriteHeader should NOT have been called
	if w.Code != http.StatusOK {
		// httptest.NewRecorder defaults to 200, and we should not have overwritten it
		t.Errorf("unexpected status code: %d", w.Code)
	}
}
