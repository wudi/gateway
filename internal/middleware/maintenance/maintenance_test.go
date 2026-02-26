package maintenance

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestDefaultResponse(t *testing.T) {
	cm := New(config.MaintenanceConfig{Enabled: true})

	req := httptest.NewRequest("GET", "/api/test", nil)
	if !cm.ShouldBlock(req) {
		t.Fatal("expected request to be blocked")
	}

	rec := httptest.NewRecorder()
	cm.WriteResponse(rec)

	if rec.Code != 503 {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
	if body := rec.Body.String(); body == "" {
		t.Error("expected non-empty body")
	}
}

func TestCustomResponse(t *testing.T) {
	cm := New(config.MaintenanceConfig{
		Enabled:     true,
		StatusCode:  418,
		Body:        "<h1>Down for maintenance</h1>",
		ContentType: "text/html",
		RetryAfter:  "3600",
		Headers: map[string]string{
			"X-Custom": "value",
		},
	})

	rec := httptest.NewRecorder()
	cm.WriteResponse(rec)

	if rec.Code != 418 {
		t.Errorf("expected 418, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("expected text/html, got %q", ct)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "3600" {
		t.Errorf("expected Retry-After=3600, got %q", ra)
	}
	if xc := rec.Header().Get("X-Custom"); xc != "value" {
		t.Errorf("expected X-Custom=value, got %q", xc)
	}
}

func TestDisabled(t *testing.T) {
	cm := New(config.MaintenanceConfig{Enabled: false})

	req := httptest.NewRequest("GET", "/api/test", nil)
	if cm.ShouldBlock(req) {
		t.Fatal("expected request to NOT be blocked when disabled")
	}
}

func TestEnableDisable(t *testing.T) {
	cm := New(config.MaintenanceConfig{Enabled: false})

	req := httptest.NewRequest("GET", "/", nil)
	if cm.ShouldBlock(req) {
		t.Fatal("should not block when disabled")
	}

	cm.Enable()
	if !cm.ShouldBlock(req) {
		t.Fatal("should block after enabling")
	}

	cm.Disable()
	if cm.ShouldBlock(req) {
		t.Fatal("should not block after disabling")
	}
}

func TestExcludePaths(t *testing.T) {
	cm := New(config.MaintenanceConfig{
		Enabled:      true,
		ExcludePaths: []string{"/health", "/health/*", "/ready"},
	})

	tests := []struct {
		path    string
		blocked bool
	}{
		{"/health", false},
		{"/ready", false},
		{"/health/deep", false},
		{"/api/test", true},
		{"/", true},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", tt.path, nil)
		if got := cm.ShouldBlock(req); got != tt.blocked {
			t.Errorf("path %s: expected blocked=%v, got %v", tt.path, tt.blocked, got)
		}
	}
}

func TestExcludeIPs(t *testing.T) {
	cm := New(config.MaintenanceConfig{
		Enabled:    true,
		ExcludeIPs: []string{"10.0.0.0/8", "192.168.1.100"},
	})

	tests := []struct {
		remoteAddr string
		blocked    bool
	}{
		{"10.0.0.1:12345", false},     // CIDR match
		{"10.255.0.1:12345", false},   // CIDR match
		{"192.168.1.100:80", false},   // exact IP match
		{"192.168.1.101:80", true},    // not excluded
		{"8.8.8.8:443", true},         // not excluded
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = tt.remoteAddr
		if got := cm.ShouldBlock(req); got != tt.blocked {
			t.Errorf("remoteAddr %s: expected blocked=%v, got %v", tt.remoteAddr, tt.blocked, got)
		}
	}
}

func TestMetrics(t *testing.T) {
	cm := New(config.MaintenanceConfig{
		Enabled:      true,
		ExcludePaths: []string{"/health"},
	})

	// Blocked request
	req1 := httptest.NewRequest("GET", "/api/test", nil)
	cm.ShouldBlock(req1)

	// Bypassed request
	req2 := httptest.NewRequest("GET", "/health", nil)
	cm.ShouldBlock(req2)

	snap := cm.Snapshot()
	if snap.TotalBlocked != 1 {
		t.Errorf("expected 1 blocked, got %d", snap.TotalBlocked)
	}
	if snap.TotalBypassed != 1 {
		t.Errorf("expected 1 bypassed, got %d", snap.TotalBypassed)
	}
	if !snap.Enabled {
		t.Error("expected enabled=true")
	}
}

func TestMergeMaintenanceConfig(t *testing.T) {
	global := config.MaintenanceConfig{
		Enabled:    true,
		StatusCode: 503,
		Body:       "global body",
		RetryAfter: "3600",
	}
	perRoute := config.MaintenanceConfig{
		Enabled:    true,
		Body:       "route body", // override
		RetryAfter: "1800",       // override
	}

	merged := MergeMaintenanceConfig(perRoute, global)
	if merged.StatusCode != 503 {
		t.Errorf("expected global status code 503, got %d", merged.StatusCode)
	}
	if merged.Body != "route body" {
		t.Errorf("expected per-route body, got %q", merged.Body)
	}
	if merged.RetryAfter != "1800" {
		t.Errorf("expected per-route retry-after, got %q", merged.RetryAfter)
	}
}

func TestMaintenanceByRoute(t *testing.T) {
	m := NewMaintenanceByRoute()
	m.AddRoute("api", config.MaintenanceConfig{Enabled: true})
	m.AddRoute("web", config.MaintenanceConfig{Enabled: false})

	if h := m.GetMaintenance("api"); h == nil {
		t.Fatal("expected api maintenance")
	} else if !h.IsEnabled() {
		t.Fatal("expected api maintenance enabled")
	}

	if h := m.GetMaintenance("web"); h == nil {
		t.Fatal("expected web maintenance")
	} else if h.IsEnabled() {
		t.Fatal("expected web maintenance disabled")
	}

	if h := m.GetMaintenance("unknown"); h != nil {
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
	cm := New(config.MaintenanceConfig{
		Enabled:      true,
		ExcludePaths: []string{"/health"},
	})

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cm.ShouldBlock(r) {
			cm.WriteResponse(w)
			return
		}
		backend.ServeHTTP(w, r)
	})

	// Blocked request
	req1 := httptest.NewRequest("GET", "/api/data", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != 503 {
		t.Errorf("expected 503, got %d", rec1.Code)
	}

	// Bypassed request
	req2 := httptest.NewRequest("GET", "/health", nil)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != 200 {
		t.Errorf("expected 200, got %d", rec2.Code)
	}
}
