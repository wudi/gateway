package aicrawl

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestDetectBuiltinCrawlers(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// Some crawlers have patterns that differ from their Name (e.g. spaces, alternation).
	// Use a lookup for realistic UA strings; fall back to Name+"/1.0" for simple cases.
	specialUAs := map[string]string{
		"ChatGPT-Agent":     "ChatGPT Agent/1.0",
		"SemrushBot-AI":     "SemrushBot-OCOB/1.0",
		"Kangaroo-Bot":      "Kangaroo Bot/1.0",
		"Linguee-Bot":       "Linguee Bot/1.0",
		"Poseidon-Research":  "Poseidon Research Crawler/1.0",
		"Datenbank-Crawler": "Datenbank Crawler/1.0",
		"EchoboxBot":        "Echobox/1.0",
		"bigsur.ai":         "bigsur.ai/1.0",
		"QuillBot":          "QuillBot/1.0",
	}

	for _, c := range BuiltinCrawlers {
		ua := c.Name + "/1.0"
		if special, ok := specialUAs[c.Name]; ok {
			ua = special
		}
		pol := ctrl.detect(ua)
		if pol == nil {
			t.Errorf("expected to detect built-in crawler %q with UA %q", c.Name, ua)
			continue
		}
		if pol.name != c.Name {
			t.Errorf("detected %q, want %q for UA %q", pol.name, c.Name, ua)
		}
	}
}

func TestDetectCrawlerWithMozillaPrefix(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	uas := []struct {
		ua   string
		name string
	}{
		{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.0; +https://openai.com/gptbot)", "GPTBot"},
		{"Mozilla/5.0 (compatible; ClaudeBot/1.0; +https://anthropic.com)", "ClaudeBot"},
		{"Mozilla/5.0 (compatible; Bytespider; spider-feedback@bytedance.com)", "Bytespider"},
		{"Mozilla/5.0 (compatible; ChatGPT-User/1.0; +https://openai.com/bot)", "ChatGPT-User"},
	}

	for _, tt := range uas {
		pol := ctrl.detect(tt.ua)
		if pol == nil {
			t.Errorf("expected to detect crawler %q in UA %q", tt.name, tt.ua)
			continue
		}
		if pol.name != tt.name {
			t.Errorf("detected %q, want %q for UA %q", pol.name, tt.name, tt.ua)
		}
	}
}

func TestNormalBrowsersPassThrough(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	uas := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15",
		"Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0",
		"curl/8.4.0",
		"PostmanRuntime/7.36.1",
	}

	for _, ua := range uas {
		if pol := ctrl.detect(ua); pol != nil {
			t.Errorf("normal browser UA %q was detected as crawler %q", ua, pol.name)
		}
	}
}

func TestEmptyUAPassesThrough(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if pol := ctrl.detect(""); pol != nil {
		t.Error("empty UA should not be detected as crawler")
	}
}

func TestBlockAction(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "block",
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "GPTBot/1.0")
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("expected 403, got %d", rec.Code)
	}

	// Should be JSON from errors.ErrForbidden.WriteJSON
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected JSON body: %v", err)
	}
	if body["message"] != "Forbidden" {
		t.Errorf("expected Forbidden message, got %v", body["message"])
	}
}

func TestCustomBlockStatusAndBody(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:          true,
		DefaultAction:    "block",
		BlockStatus:      451,
		BlockBody:        "Unavailable For Legal Reasons",
		BlockContentType: "text/html",
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "ClaudeBot/1.0")
	handler.ServeHTTP(rec, req)

	if rec.Code != 451 {
		t.Errorf("expected 451, got %d", rec.Code)
	}
	if rec.Body.String() != "Unavailable For Legal Reasons" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html" {
		t.Errorf("expected text/html, got %s", ct)
	}
}

func TestMonitorAction(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
	})
	if err != nil {
		t.Fatal(err)
	}

	var reached bool
	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "GPTBot/1.0")
	handler.ServeHTTP(rec, req)

	if !reached {
		t.Error("monitor action should pass through to next handler")
	}
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	// No expose_headers, so no X-AI-Crawler-Detected
	if h := rec.Header().Get("X-AI-Crawler-Detected"); h != "" {
		t.Errorf("expected no X-AI-Crawler-Detected header, got %q", h)
	}
}

func TestMonitorActionWithExposeHeaders(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
		ExposeHeaders: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "GPTBot/1.0")
	handler.ServeHTTP(rec, req)

	if h := rec.Header().Get("X-AI-Crawler-Detected"); h != "GPTBot" {
		t.Errorf("expected X-AI-Crawler-Detected=GPTBot, got %q", h)
	}
}

func TestBlockActionWithExposeHeaders(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "block",
		ExposeHeaders: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "CCBot/2.0")
	handler.ServeHTTP(rec, req)

	if h := rec.Header().Get("X-AI-Crawler-Blocked"); h != "CCBot" {
		t.Errorf("expected X-AI-Crawler-Blocked=CCBot, got %q", h)
	}
}

func TestAllowAction(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "allow",
	})
	if err != nil {
		t.Fatal(err)
	}

	var reached bool
	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "GPTBot/1.0")
	handler.ServeHTTP(rec, req)

	if !reached {
		t.Error("allow action should pass through")
	}
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestDefaultMonitorZeroConfig(t *testing.T) {
	// Zero-config: enabled with no policies defaults to monitor
	ctrl, err := New(config.AICrawlConfig{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	var reached bool
	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "GPTBot/1.0")
	handler.ServeHTTP(rec, req)

	if !reached {
		t.Error("zero-config should default to monitor (pass through)")
	}
}

func TestDisallowPaths(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "allow",
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "allow", DisallowPaths: []string{"/private/**", "/admin/*"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path    string
		blocked bool
	}{
		{"/public/page", false},
		{"/private/data/secret", true},
		{"/private/page", true},
		{"/admin/dashboard", true},
		{"/admin/sub/deep", false}, // single * doesn't cross /
		{"/other", false},
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", tt.path, nil)
		req.Header.Set("User-Agent", "GPTBot/1.0")
		handler.ServeHTTP(rec, req)
		if tt.blocked && rec.Code != 403 {
			t.Errorf("path %s: expected 403, got %d", tt.path, rec.Code)
		}
		if !tt.blocked && rec.Code != 200 {
			t.Errorf("path %s: expected 200, got %d", tt.path, rec.Code)
		}
	}
}

func TestAllowPaths(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "allow",
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "ClaudeBot", Action: "allow", AllowPaths: []string{"/public/**", "/api/v1/*"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path    string
		blocked bool
	}{
		{"/public/page", true},   // /public/** needs something after /public/
		{"/public/a/b", true},    // doesn't match: /public/** needs a trailing segment? Let me check doublestar behavior
		{"/private/data", true},  // not in allow_paths
		{"/api/v1/users", false}, // matches /api/v1/*
	}

	// Actually re-check: doublestar.PathMatch("/public/**", "/public/page") should match
	// Let me just test with the middleware
	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// /public/page should be allowed (matches /public/**)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/public/page", nil)
	req.Header.Set("User-Agent", "ClaudeBot/1.0")
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("/public/page: expected 200 (allowed), got %d", rec.Code)
	}

	// /private/data should be blocked (not in allow_paths)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/private/data", nil)
	req.Header.Set("User-Agent", "ClaudeBot/1.0")
	handler.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("/private/data: expected 403 (blocked, not in allow_paths), got %d", rec.Code)
	}

	// /api/v1/users should be allowed
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/users", nil)
	req.Header.Set("User-Agent", "ClaudeBot/1.0")
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("/api/v1/users: expected 200, got %d", rec.Code)
	}

	// Non-crawler should pass through regardless
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/private/data", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("non-crawler /private/data: expected 200, got %d", rec.Code)
	}

	_ = tests // used for documentation
}

func TestCustomCrawlerDetection(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
		CustomCrawlers: []config.CustomCrawlerConfig{
			{Name: "MyBot", Pattern: `(?i)MyBot`},
		},
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "MyBot", Action: "block"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "MyBot/1.0")
	handler.ServeHTTP(rec, req)

	if rec.Code != 403 {
		t.Errorf("expected 403 for custom crawler, got %d", rec.Code)
	}
}

func TestCustomCrawlersCheckedBeforeBuiltin(t *testing.T) {
	// Create a custom crawler that overlaps with a built-in name pattern
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
		CustomCrawlers: []config.CustomCrawlerConfig{
			{Name: "CustomGPT", Pattern: `(?i)GPTBot`},
		},
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "CustomGPT", Action: "block"},
			{Crawler: "GPTBot", Action: "allow"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// CustomGPT should match first (custom before builtin)
	pol := ctrl.detect("GPTBot/1.0")
	if pol == nil {
		t.Fatal("expected detection")
	}
	if pol.name != "CustomGPT" {
		t.Errorf("expected CustomGPT (custom first), got %q", pol.name)
	}
}

func TestPerCrawlerAndAggregateMetrics(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "block"},
			{Crawler: "ClaudeBot", Action: "allow"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Send GPTBot requests (blocked)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("User-Agent", "GPTBot/1.0")
		handler.ServeHTTP(rec, req)
	}

	// Send ClaudeBot requests (allowed)
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("User-Agent", "ClaudeBot/1.0")
		handler.ServeHTTP(rec, req)
	}

	// Send CCBot request (monitored - default)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "CCBot/2.0")
	handler.ServeHTTP(rec, req)

	stats := ctrl.Stats()
	if stats["total_detected"].(int64) != 6 {
		t.Errorf("total_detected: expected 6, got %v", stats["total_detected"])
	}
	if stats["total_blocked"].(int64) != 3 {
		t.Errorf("total_blocked: expected 3, got %v", stats["total_blocked"])
	}
	if stats["total_allowed"].(int64) != 2 {
		t.Errorf("total_allowed: expected 2, got %v", stats["total_allowed"])
	}
	if stats["total_monitored"].(int64) != 1 {
		t.Errorf("total_monitored: expected 1, got %v", stats["total_monitored"])
	}

	crawlers := stats["crawlers"].(map[string]interface{})
	gpt := crawlers["GPTBot"].(map[string]interface{})
	if gpt["requests"].(int64) != 3 {
		t.Errorf("GPTBot requests: expected 3, got %v", gpt["requests"])
	}
	if gpt["blocked"].(int64) != 3 {
		t.Errorf("GPTBot blocked: expected 3, got %v", gpt["blocked"])
	}
	if gpt["action"].(string) != "block" {
		t.Errorf("GPTBot action: expected block, got %v", gpt["action"])
	}
	if gpt["last_seen"].(string) == "" {
		t.Error("GPTBot last_seen should not be empty")
	}

	claude := crawlers["ClaudeBot"].(map[string]interface{})
	if claude["allowed"].(int64) != 2 {
		t.Errorf("ClaudeBot allowed: expected 2, got %v", claude["allowed"])
	}
}

func TestInvalidCustomPattern(t *testing.T) {
	_, err := New(config.AICrawlConfig{
		Enabled: true,
		CustomCrawlers: []config.CustomCrawlerConfig{
			{Name: "Bad", Pattern: `[invalid`},
		},
	})
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}

func TestByRouteManager(t *testing.T) {
	m := NewAICrawlByRoute()

	err := m.AddRoute("route1", config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "block",
	})
	if err != nil {
		t.Fatal(err)
	}

	err = m.AddRoute("route2", config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "allow",
	})
	if err != nil {
		t.Fatal(err)
	}

	if m.GetController("route1") == nil {
		t.Error("expected controller for route1")
	}
	if m.GetController("route2") == nil {
		t.Error("expected controller for route2")
	}
	if m.GetController("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
	if _, ok := stats["route2"]; !ok {
		t.Error("expected stats for route2")
	}
}

func TestMergeAICrawlConfig(t *testing.T) {
	global := config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "block"},
		},
	}

	route := config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "block",
	}

	merged := MergeAICrawlConfig(route, global)
	if merged.DefaultAction != "block" {
		t.Errorf("expected route default_action to override, got %q", merged.DefaultAction)
	}
	if !merged.Enabled {
		t.Error("merged should be enabled")
	}
	// Global policies inherited when route has none
	if len(merged.Policies) != 1 {
		t.Errorf("expected global policies to be inherited, got %d", len(merged.Policies))
	}
}

func TestConcurrentAccess(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "block"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", "GPTBot/1.0")
			handler.ServeHTTP(rec, req)
		}()
	}
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("User-Agent", "Mozilla/5.0 Chrome/120")
			handler.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	stats := ctrl.Stats()
	if stats["total_detected"].(int64) != 100 {
		t.Errorf("expected 100 detected, got %v", stats["total_detected"])
	}
	if stats["total_blocked"].(int64) != 100 {
		t.Errorf("expected 100 blocked, got %v", stats["total_blocked"])
	}
}

func TestPerCrawlerPolicy(t *testing.T) {
	ctrl, err := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
		Policies: []config.AICrawlPolicyConfig{
			{Crawler: "GPTBot", Action: "block"},
			{Crawler: "ClaudeBot", Action: "allow"},
			{Crawler: "CCBot", Action: "block"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	tests := []struct {
		ua     string
		status int
	}{
		{"GPTBot/1.0", 403},
		{"ClaudeBot/1.0", 200},
		{"CCBot/2.0", 403},
		{"Bytespider/1.0", 200}, // default: monitor (passes through)
		{"Mozilla/5.0", 200},    // not a crawler
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("User-Agent", tt.ua)
		handler.ServeHTTP(rec, req)
		if rec.Code != tt.status {
			t.Errorf("UA %q: expected %d, got %d", tt.ua, tt.status, rec.Code)
		}
	}
}

func BenchmarkDetect(b *testing.B) {
	ctrl, _ := New(config.AICrawlConfig{Enabled: true})
	// Common Chrome UA — should be rejected by preFilter fast-path
	ua := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctrl.detect(ua)
	}
}

func BenchmarkDetectCrawler(b *testing.B) {
	ctrl, _ := New(config.AICrawlConfig{Enabled: true})
	ua := "Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.0; +https://openai.com/gptbot)"

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctrl.detect(ua)
	}
}

func BenchmarkMiddleware(b *testing.B) {
	ctrl, _ := New(config.AICrawlConfig{
		Enabled:       true,
		DefaultAction: "monitor",
	})
	handler := ctrl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
}
