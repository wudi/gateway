package securityheaders

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestDefaultHeaders(t *testing.T) {
	// Enabled with no explicit values → should still get X-Content-Type-Options: nosniff
	c := New(config.SecurityHeadersConfig{Enabled: true})
	rec := httptest.NewRecorder()
	c.Apply(rec.Header())

	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected X-Content-Type-Options=nosniff, got %q", got)
	}
}

func TestAllHeaders(t *testing.T) {
	cfg := config.SecurityHeadersConfig{
		Enabled:                       true,
		StrictTransportSecurity:       "max-age=31536000; includeSubDomains",
		ContentSecurityPolicy:         "default-src 'self'",
		XContentTypeOptions:           "nosniff",
		XFrameOptions:                 "DENY",
		ReferrerPolicy:                "strict-origin-when-cross-origin",
		PermissionsPolicy:             "camera=(), microphone=()",
		CrossOriginOpenerPolicy:       "same-origin",
		CrossOriginEmbedderPolicy:     "require-corp",
		CrossOriginResourcePolicy:     "same-origin",
		XPermittedCrossDomainPolicies: "none",
	}
	c := New(cfg)
	rec := httptest.NewRecorder()
	c.Apply(rec.Header())

	expected := map[string]string{
		"Strict-Transport-Security":       "max-age=31536000; includeSubDomains",
		"Content-Security-Policy":         "default-src 'self'",
		"X-Content-Type-Options":          "nosniff",
		"X-Frame-Options":                 "DENY",
		"Referrer-Policy":                 "strict-origin-when-cross-origin",
		"Permissions-Policy":              "camera=(), microphone=()",
		"Cross-Origin-Opener-Policy":      "same-origin",
		"Cross-Origin-Embedder-Policy":    "require-corp",
		"Cross-Origin-Resource-Policy":    "same-origin",
		"X-Permitted-Cross-Domain-Policies": "none",
	}

	for name, want := range expected {
		if got := rec.Header().Get(name); got != want {
			t.Errorf("%s: expected %q, got %q", name, want, got)
		}
	}
}

func TestCustomHeaders(t *testing.T) {
	cfg := config.SecurityHeadersConfig{
		Enabled: true,
		CustomHeaders: map[string]string{
			"X-Custom-One": "value1",
			"X-Custom-Two": "value2",
		},
	}
	c := New(cfg)
	rec := httptest.NewRecorder()
	c.Apply(rec.Header())

	if got := rec.Header().Get("X-Custom-One"); got != "value1" {
		t.Errorf("expected X-Custom-One=value1, got %q", got)
	}
	if got := rec.Header().Get("X-Custom-Two"); got != "value2" {
		t.Errorf("expected X-Custom-Two=value2, got %q", got)
	}
}

func TestMetrics(t *testing.T) {
	c := New(config.SecurityHeadersConfig{Enabled: true})
	rec := httptest.NewRecorder()
	c.Apply(rec.Header())
	c.Apply(rec.Header())

	snap := c.Snapshot()
	if snap.TotalRequests != 2 {
		t.Errorf("expected 2 requests, got %d", snap.TotalRequests)
	}
	if snap.HeaderCount < 1 {
		t.Errorf("expected at least 1 header, got %d", snap.HeaderCount)
	}
}

func TestMergeSecurityHeadersConfig(t *testing.T) {
	global := config.SecurityHeadersConfig{
		Enabled:                 true,
		StrictTransportSecurity: "max-age=31536000",
		XFrameOptions:          "DENY",
		ReferrerPolicy:         "no-referrer",
	}
	perRoute := config.SecurityHeadersConfig{
		Enabled:       true,
		XFrameOptions: "SAMEORIGIN", // override
	}

	merged := MergeSecurityHeadersConfig(perRoute, global)
	if merged.StrictTransportSecurity != "max-age=31536000" {
		t.Errorf("expected global HSTS, got %q", merged.StrictTransportSecurity)
	}
	if merged.XFrameOptions != "SAMEORIGIN" {
		t.Errorf("expected per-route XFO override, got %q", merged.XFrameOptions)
	}
	if merged.ReferrerPolicy != "no-referrer" {
		t.Errorf("expected global referrer policy, got %q", merged.ReferrerPolicy)
	}
}

func TestMergeCustomHeaders(t *testing.T) {
	global := config.SecurityHeadersConfig{
		Enabled: true,
		CustomHeaders: map[string]string{
			"X-Global": "g1",
			"X-Shared": "global-val",
		},
	}
	perRoute := config.SecurityHeadersConfig{
		Enabled: true,
		CustomHeaders: map[string]string{
			"X-Shared": "route-val",
			"X-Route":  "r1",
		},
	}

	merged := MergeSecurityHeadersConfig(perRoute, global)
	if merged.CustomHeaders["X-Global"] != "g1" {
		t.Errorf("expected global custom header preserved")
	}
	if merged.CustomHeaders["X-Shared"] != "route-val" {
		t.Errorf("expected per-route override for shared key")
	}
	if merged.CustomHeaders["X-Route"] != "r1" {
		t.Errorf("expected per-route custom header added")
	}
}

func TestSecurityHeadersByRoute(t *testing.T) {
	m := NewSecurityHeadersByRoute()
	m.AddRoute("api", config.SecurityHeadersConfig{
		Enabled:       true,
		XFrameOptions: "DENY",
	})
	m.AddRoute("web", config.SecurityHeadersConfig{
		Enabled:         true,
		ReferrerPolicy:  "no-referrer",
	})

	if h := m.GetHeaders("api"); h == nil {
		t.Fatal("expected api headers")
	}
	if h := m.GetHeaders("unknown"); h != nil {
		t.Fatal("expected nil for unknown route")
	}

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := m.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
}

func TestMiddlewareIntegration(t *testing.T) {
	cfg := config.SecurityHeadersConfig{
		Enabled:                 true,
		StrictTransportSecurity: "max-age=31536000",
		XFrameOptions:          "DENY",
	}
	c := New(cfg)

	// Simulate middleware: apply headers before writing response
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.Apply(w.Header())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if got := rec.Header().Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Errorf("expected HSTS header, got %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("expected XFO header, got %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("expected default nosniff, got %q", got)
	}
}

func TestOverrideDefaultXContentTypeOptions(t *testing.T) {
	// User can override the default "nosniff"
	cfg := config.SecurityHeadersConfig{
		Enabled:             true,
		XContentTypeOptions: "custom-value",
	}
	c := New(cfg)
	rec := httptest.NewRecorder()
	c.Apply(rec.Header())

	if got := rec.Header().Get("X-Content-Type-Options"); got != "custom-value" {
		t.Errorf("expected custom-value, got %q", got)
	}
}
