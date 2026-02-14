package botdetect

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestBotDetector_DenyMatch(t *testing.T) {
	bd, err := New(config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`(?i)googlebot`, `(?i)bingbot`},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		ua      string
		allowed bool
	}{
		{"Mozilla/5.0 (compatible; Googlebot/2.1)", false},
		{"Mozilla/5.0 (compatible; bingbot/2.0)", false},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64)", true},
		{"curl/7.68.0", true},
		{"", true},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		if tt.ua != "" {
			r.Header.Set("User-Agent", tt.ua)
		}
		if got := bd.Check(r); got != tt.allowed {
			t.Errorf("Check(%q) = %v, want %v", tt.ua, got, tt.allowed)
		}
	}
}

func TestBotDetector_AllowOverride(t *testing.T) {
	bd, err := New(config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`(?i)bot`},
		Allow:   []string{`(?i)goodbot`},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		ua      string
		allowed bool
	}{
		{"badbot/1.0", false},
		{"goodbot/1.0", true},
		{"Mozilla/5.0", true},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("User-Agent", tt.ua)
		if got := bd.Check(r); got != tt.allowed {
			t.Errorf("Check(%q) = %v, want %v", tt.ua, got, tt.allowed)
		}
	}
}

func TestBotDetector_InvalidRegex(t *testing.T) {
	_, err := New(config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`[invalid`},
	})
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestBotDetector_Middleware(t *testing.T) {
	bd, _ := New(config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`(?i)badbot`},
	})

	handler := bd.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Allowed request
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Blocked request
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "badbot/1.0")
	handler.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("expected 403, got %d", rec.Code)
	}
}

func TestBotDetector_BlockedCounter(t *testing.T) {
	bd, _ := New(config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`bot`},
	})

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", "bot/1.0")
	bd.Check(r)
	bd.Check(r)

	if bd.Blocked() != 2 {
		t.Errorf("expected 2 blocked, got %d", bd.Blocked())
	}
}

func TestBotDetectByRoute(t *testing.T) {
	m := NewBotDetectByRoute()
	err := m.AddRoute("route1", config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`bot`},
	})
	if err != nil {
		t.Fatal(err)
	}

	if m.GetDetector("route1") == nil {
		t.Error("expected detector for route1")
	}
	if m.GetDetector("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
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

func TestMergeBotDetectionConfig(t *testing.T) {
	global := config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`globalbot`},
		Allow:   []string{`globalallow`},
	}

	// Per-route overrides deny but inherits allow
	route := config.BotDetectionConfig{
		Enabled: true,
		Deny:    []string{`routebot`},
	}

	merged := MergeBotDetectionConfig(route, global)
	if len(merged.Deny) != 1 || merged.Deny[0] != "routebot" {
		t.Errorf("expected route deny to override, got %v", merged.Deny)
	}
	if len(merged.Allow) != 1 || merged.Allow[0] != "globalallow" {
		t.Errorf("expected global allow to be inherited, got %v", merged.Allow)
	}
}
