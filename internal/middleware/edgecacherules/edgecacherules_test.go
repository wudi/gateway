package edgecacherules

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func boolPtr(b bool) *bool { return &b }

func TestNew_BasicRule(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match: config.EdgeCacheMatch{
					StatusCodes: []int{200},
				},
				SMaxAge: 3600,
				MaxAge:  300,
			},
		},
	})

	if len(ecr.rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(ecr.rules))
	}
}

func TestEvaluate_StatusCodeMatch(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200, 301}},
				SMaxAge: 3600,
				MaxAge:  300,
			},
		},
	})

	cc, _, _, matched := ecr.Evaluate(200, "text/html", "/page")
	if !matched {
		t.Fatal("expected match for status 200")
	}
	if cc != "public, max-age=300, s-maxage=3600" {
		t.Errorf("unexpected cache-control: %s", cc)
	}

	_, _, _, matched = ecr.Evaluate(404, "text/html", "/page")
	if matched {
		t.Error("expected no match for status 404")
	}
}

func TestEvaluate_ContentTypeMatch(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{ContentTypes: []string{"text/html", "application/json"}},
				SMaxAge: 600,
			},
		},
	})

	_, _, _, matched := ecr.Evaluate(200, "text/html; charset=utf-8", "/page")
	if !matched {
		t.Error("expected match for text/html with charset")
	}

	_, _, _, matched = ecr.Evaluate(200, "application/json", "/api")
	if !matched {
		t.Error("expected match for application/json")
	}

	_, _, _, matched = ecr.Evaluate(200, "image/png", "/img.png")
	if matched {
		t.Error("expected no match for image/png")
	}
}

func TestEvaluate_PathPatternMatch(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{PathPatterns: []string{"/static/*", "/assets/*"}},
				SMaxAge: 86400,
			},
		},
	})

	_, _, _, matched := ecr.Evaluate(200, "text/css", "/static/style.css")
	if !matched {
		t.Error("expected match for /static/style.css")
	}

	_, _, _, matched = ecr.Evaluate(200, "text/css", "/api/data")
	if matched {
		t.Error("expected no match for /api/data")
	}
}

func TestEvaluate_CombinedMatch(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match: config.EdgeCacheMatch{
					StatusCodes:  []int{200},
					ContentTypes: []string{"text/html"},
					PathPatterns: []string{"/pages/*"},
				},
				MaxAge: 120,
			},
		},
	})

	// All conditions match
	_, _, _, matched := ecr.Evaluate(200, "text/html", "/pages/about")
	if !matched {
		t.Error("expected match when all conditions match")
	}

	// Status doesn't match
	_, _, _, matched = ecr.Evaluate(404, "text/html", "/pages/about")
	if matched {
		t.Error("expected no match when status doesn't match")
	}

	// Content type doesn't match
	_, _, _, matched = ecr.Evaluate(200, "application/json", "/pages/about")
	if matched {
		t.Error("expected no match when content type doesn't match")
	}

	// Path doesn't match
	_, _, _, matched = ecr.Evaluate(200, "text/html", "/api/data")
	if matched {
		t.Error("expected no match when path doesn't match")
	}
}

func TestEvaluate_FirstMatchWins(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{PathPatterns: []string{"/static/*"}},
				SMaxAge: 86400,
			},
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 300,
			},
		},
	})

	// Request matching both rules — first rule should win
	cc, _, _, matched := ecr.Evaluate(200, "text/css", "/static/style.css")
	if !matched {
		t.Fatal("expected match")
	}
	if cc != "public, s-maxage=86400" {
		t.Errorf("expected first rule's cache-control, got: %s", cc)
	}
}

func TestEvaluate_NoStore(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{PathPatterns: []string{"/private/*"}},
				NoStore: true,
			},
		},
	})

	cc, _, _, matched := ecr.Evaluate(200, "text/html", "/private/settings")
	if !matched {
		t.Fatal("expected match")
	}
	if cc != "no-store" {
		t.Errorf("expected no-store, got: %s", cc)
	}
}

func TestEvaluate_Private(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				Private: true,
				MaxAge:  60,
			},
		},
	})

	cc, _, _, matched := ecr.Evaluate(200, "text/html", "/page")
	if !matched {
		t.Fatal("expected match")
	}
	if cc != "private, max-age=60" {
		t.Errorf("expected private, max-age=60, got: %s", cc)
	}
}

func TestEvaluate_RawCacheControl(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:        config.EdgeCacheMatch{StatusCodes: []int{200}},
				CacheControl: "public, max-age=3600, stale-while-revalidate=60",
			},
		},
	})

	cc, _, _, matched := ecr.Evaluate(200, "text/html", "/page")
	if !matched {
		t.Fatal("expected match")
	}
	if cc != "public, max-age=3600, stale-while-revalidate=60" {
		t.Errorf("unexpected cache-control: %s", cc)
	}
}

func TestEvaluate_Vary(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 300,
				Vary:    []string{"Accept", "Accept-Encoding"},
			},
		},
	})

	_, vary, _, matched := ecr.Evaluate(200, "text/html", "/page")
	if !matched {
		t.Fatal("expected match")
	}
	if vary != "Accept, Accept-Encoding" {
		t.Errorf("unexpected vary: %s", vary)
	}
}

func TestEvaluate_OverrideDefault(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 300,
			},
		},
	})

	_, _, override, matched := ecr.Evaluate(200, "text/html", "/page")
	if !matched {
		t.Fatal("expected match")
	}
	if !override {
		t.Error("expected override=true by default")
	}
}

func TestEvaluate_OverrideFalse(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:    config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge:  300,
				Override: boolPtr(false),
			},
		},
	})

	_, _, override, matched := ecr.Evaluate(200, "text/html", "/page")
	if !matched {
		t.Fatal("expected match")
	}
	if override {
		t.Error("expected override=false")
	}
}

func TestMiddleware_SetsHeaders(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 3600,
				Vary:    []string{"Accept"},
			},
		},
	})

	handler := ecr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/page", nil)
	handler.ServeHTTP(w, r)

	if w.Header().Get("Cache-Control") != "public, s-maxage=3600" {
		t.Errorf("expected Cache-Control header, got: %s", w.Header().Get("Cache-Control"))
	}
	if w.Header().Get("Vary") != "Accept" {
		t.Errorf("expected Vary: Accept, got: %s", w.Header().Get("Vary"))
	}
}

func TestMiddleware_OverrideTrue(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 3600,
			},
		},
	})

	handler := ecr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/page", nil)
	handler.ServeHTTP(w, r)

	// Override=true (default): should replace backend Cache-Control
	if w.Header().Get("Cache-Control") != "public, s-maxage=3600" {
		t.Errorf("expected overridden Cache-Control, got: %s", w.Header().Get("Cache-Control"))
	}
}

func TestMiddleware_OverrideFalse_NoBackendHeader(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:    config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge:  3600,
				Override: boolPtr(false),
			},
		},
	})

	handler := ecr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/page", nil)
	handler.ServeHTTP(w, r)

	// No backend Cache-Control, so rule should apply
	if w.Header().Get("Cache-Control") != "public, s-maxage=3600" {
		t.Errorf("expected Cache-Control, got: %s", w.Header().Get("Cache-Control"))
	}
}

func TestMiddleware_OverrideFalse_WithBackendHeader(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:    config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge:  3600,
				Override: boolPtr(false),
			},
		},
	})

	handler := ecr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/page", nil)
	handler.ServeHTTP(w, r)

	// Override=false and backend set Cache-Control: should preserve backend value
	if w.Header().Get("Cache-Control") != "private, max-age=60" {
		t.Errorf("expected backend Cache-Control preserved, got: %s", w.Header().Get("Cache-Control"))
	}
}

func TestMiddleware_NoMatch(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 3600,
			},
		},
	})

	handler := ecr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/missing", nil)
	handler.ServeHTTP(w, r)

	if w.Header().Get("Cache-Control") != "" {
		t.Errorf("expected no Cache-Control for non-matching status, got: %s", w.Header().Get("Cache-Control"))
	}
}

func TestMiddleware_WriteWithoutWriteHeader(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 3600,
			},
		},
	})

	handler := ecr.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// Write without explicit WriteHeader — defaults to 200
		w.Write([]byte("hello"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/page", nil)
	handler.ServeHTTP(w, r)

	if w.Header().Get("Cache-Control") != "public, s-maxage=3600" {
		t.Errorf("expected Cache-Control for implicit 200, got: %s", w.Header().Get("Cache-Control"))
	}
}

func TestStats(t *testing.T) {
	ecr := New(config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{
				Match:   config.EdgeCacheMatch{StatusCodes: []int{200}},
				SMaxAge: 300,
			},
		},
	})

	ecr.Evaluate(200, "text/html", "/a")
	ecr.Evaluate(200, "text/html", "/b")
	ecr.Evaluate(404, "text/html", "/c") // no match

	stats := ecr.Stats()
	if stats.Applied != 2 {
		t.Errorf("expected 2 applied, got %d", stats.Applied)
	}
	if stats.RuleCount != 1 {
		t.Errorf("expected 1 rule, got %d", stats.RuleCount)
	}
}

func TestMergeEdgeCacheRulesConfig(t *testing.T) {
	global := config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{Match: config.EdgeCacheMatch{StatusCodes: []int{200}}, SMaxAge: 300},
		},
	}

	// Per-route not enabled → use global
	merged := MergeEdgeCacheRulesConfig(config.EdgeCacheRulesConfig{}, global)
	if !merged.Enabled {
		t.Error("expected global config when per-route disabled")
	}

	// Per-route enabled → use per-route
	perRoute := config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{Match: config.EdgeCacheMatch{StatusCodes: []int{404}}, NoStore: true},
		},
	}
	merged = MergeEdgeCacheRulesConfig(perRoute, global)
	if len(merged.Rules) != 1 || len(merged.Rules[0].Match.StatusCodes) != 1 || merged.Rules[0].Match.StatusCodes[0] != 404 {
		t.Error("expected per-route config to take precedence")
	}
}

func TestByRoute(t *testing.T) {
	br := NewEdgeCacheRulesByRoute()

	br.AddRoute("route1", config.EdgeCacheRulesConfig{
		Enabled: true,
		Rules: []config.EdgeCacheRule{
			{Match: config.EdgeCacheMatch{StatusCodes: []int{200}}, SMaxAge: 300},
		},
	})

	h := br.Lookup("route1")
	if h == nil {
		t.Fatal("expected handler for route1")
	}

	if br.Lookup("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	stats := br.Stats()
	if len(stats) != 1 {
		t.Errorf("expected 1 route in stats, got %d", len(stats))
	}
}
