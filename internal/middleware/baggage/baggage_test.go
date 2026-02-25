package baggage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/baggage"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/variables"
)

func setupVarContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.NewContext(r)
		ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestPropagator_HeaderSource(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "tenant", Source: "header:X-Tenant-ID", Header: "X-Backend-Tenant"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Backend-Tenant")
		w.WriteHeader(200)
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Tenant-ID", "acme-corp")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotHeader != "acme-corp" {
		t.Errorf("expected X-Backend-Tenant=acme-corp, got %s", gotHeader)
	}
}

func TestPropagator_QuerySource(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "region", Source: "query:region", Header: "X-Region"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Region")
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test?region=us-east-1", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotHeader != "us-east-1" {
		t.Errorf("expected X-Region=us-east-1, got %s", gotHeader)
	}
}

func TestPropagator_CookieSource(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "session", Source: "cookie:sid", Header: "X-Session-ID"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Session-ID")
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "abc123"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotHeader != "abc123" {
		t.Errorf("expected X-Session-ID=abc123, got %s", gotHeader)
	}
}

func TestPropagator_StaticSource(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "env", Source: "static:production", Header: "X-Environment"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Environment")
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotHeader != "production" {
		t.Errorf("expected X-Environment=production, got %s", gotHeader)
	}
}

func TestPropagator_JWTClaimSource(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "user_id", Source: "jwt_claim:sub", Header: "X-User-ID"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-User-ID")
	})

	// Set up context with JWT claims
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.NewContext(r)
		varCtx.Identity = &variables.Identity{
			Claims: map[string]interface{}{"sub": "user-42"},
		}
		ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
		p.Middleware()(inner).ServeHTTP(w, r.WithContext(ctx))
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotHeader != "user-42" {
		t.Errorf("expected X-User-ID=user-42, got %s", gotHeader)
	}
}

func TestPropagator_EmptyValue(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "missing", Source: "header:X-Missing", Header: "X-Backend-Missing"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Backend-Missing")
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotHeader != "" {
		t.Errorf("expected empty header, got %s", gotHeader)
	}
}

func TestPropagator_CustomVarContext(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "tenant", Source: "header:X-Tenant", Header: "X-Backend-Tenant"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotCustom string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vc := variables.GetFromRequest(r)
		gotCustom, _ = vc.GetCustom("tenant")
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotCustom != "acme" {
		t.Errorf("expected custom var tenant=acme, got %s", gotCustom)
	}
}

func TestPropagator_PropagatedCounter(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "env", Source: "static:test", Header: "X-Env"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	handler := setupVarContext(p.Middleware()(inner))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	if p.Propagated() != 3 {
		t.Errorf("expected propagated=3, got %d", p.Propagated())
	}
}

func TestBaggageByRoute(t *testing.T) {
	b := NewBaggageByRoute()
	err := b.AddRoute("route1", config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "env", Source: "static:test", Header: "X-Env"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if p := b.GetPropagator("route1"); p == nil {
		t.Error("expected propagator for route1")
	}
	if p := b.GetPropagator("nonexistent"); p != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := b.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}
}

func TestBaggageByRoute_Stats(t *testing.T) {
	b := NewBaggageByRoute()
	err := b.AddRoute("route1", config.BaggageConfig{
		Enabled: true,
		Tags: []config.BaggageTagDef{
			{Name: "env", Source: "static:test", Header: "X-Env"},
			{Name: "region", Source: "header:X-Region", Header: "X-Backend-Region"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stats := b.Stats()
	if len(stats) != 1 {
		t.Errorf("expected 1 stat entry, got %d", len(stats))
	}

	routeStats, ok := stats["route1"]
	if !ok {
		t.Fatal("expected stats for route1")
	}

	m, ok := routeStats.(map[string]interface{})
	if !ok {
		t.Fatal("expected map[string]interface{} stats")
	}

	if tags, ok := m["tags"].(int); !ok || tags != 2 {
		t.Errorf("expected tags=2, got %v", m["tags"])
	}
	if propagated, ok := m["propagated"].(int64); !ok || propagated != 0 {
		t.Errorf("expected propagated=0, got %v", m["propagated"])
	}
	if pt, ok := m["propagate_trace"].(bool); !ok || pt {
		t.Errorf("expected propagate_trace=false, got %v", m["propagate_trace"])
	}
	if w3c, ok := m["w3c_baggage"].(bool); !ok || w3c {
		t.Errorf("expected w3c_baggage=false, got %v", m["w3c_baggage"])
	}
}

// --- New tests for W3C baggage and propagate_trace ---

func TestPropagator_PropagateTrace(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled:        true,
		PropagateTrace: true,
		Tags: []config.BaggageTagDef{
			{Name: "env", Source: "static:test", Header: "X-Env"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotPropagateTrace bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vc := variables.GetFromRequest(r)
		gotPropagateTrace = vc.PropagateTrace
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !gotPropagateTrace {
		t.Error("expected PropagateTrace=true")
	}
}

func TestPropagator_PropagateTrace_Disabled(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled:        true,
		PropagateTrace: false,
		Tags: []config.BaggageTagDef{
			{Name: "env", Source: "static:test", Header: "X-Env"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotPropagateTrace bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vc := variables.GetFromRequest(r)
		gotPropagateTrace = vc.PropagateTrace
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if gotPropagateTrace {
		t.Error("expected PropagateTrace=false")
	}
}

func TestPropagator_W3CBaggage(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled:    true,
		W3CBaggage: true,
		Tags: []config.BaggageTagDef{
			{Name: "tenant-id", Source: "header:X-Tenant", Header: "X-Backend-Tenant"},
			{Name: "environment", Source: "static:production", Header: "X-Env"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBaggage baggage.Baggage
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBaggage = baggage.FromContext(r.Context())
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if m := gotBaggage.Member("tenant-id"); m.Value() != "acme" {
		t.Errorf("expected baggage tenant-id=acme, got %q", m.Value())
	}
	if m := gotBaggage.Member("environment"); m.Value() != "production" {
		t.Errorf("expected baggage environment=production, got %q", m.Value())
	}
}

func TestPropagator_W3CBaggage_CustomKey(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled:    true,
		W3CBaggage: true,
		Tags: []config.BaggageTagDef{
			{Name: "tenant", Source: "header:X-Tenant", Header: "X-Backend-Tenant", BaggageKey: "tenant.id"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBaggage baggage.Baggage
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBaggage = baggage.FromContext(r.Context())
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should use BaggageKey, not Name
	if m := gotBaggage.Member("tenant.id"); m.Value() != "acme" {
		t.Errorf("expected baggage tenant.id=acme, got %q", m.Value())
	}
	if m := gotBaggage.Member("tenant"); m.Value() != "" {
		t.Errorf("expected no baggage member for 'tenant', got %q", m.Value())
	}
}

func TestPropagator_W3CBaggage_EmptyValueSkipped(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled:    true,
		W3CBaggage: true,
		Tags: []config.BaggageTagDef{
			{Name: "missing", Source: "header:X-Missing", Header: "X-Backend-Missing"},
			{Name: "present", Source: "static:yes", Header: "X-Present"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBaggage baggage.Baggage
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBaggage = baggage.FromContext(r.Context())
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if m := gotBaggage.Member("missing"); m.Value() != "" {
		t.Errorf("expected no baggage for missing, got %q", m.Value())
	}
	if m := gotBaggage.Member("present"); m.Value() != "yes" {
		t.Errorf("expected baggage present=yes, got %q", m.Value())
	}
}

func TestPropagator_W3CBaggage_MergesUpstream(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled:    true,
		W3CBaggage: true,
		Tags: []config.BaggageTagDef{
			{Name: "gateway-tag", Source: "static:gw", Header: "X-GW"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBaggage baggage.Baggage
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBaggage = baggage.FromContext(r.Context())
	})

	// Set up upstream baggage in context
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.NewContext(r)
		ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)

		// Add existing upstream baggage
		m, _ := baggage.NewMember("upstream-key", "upstream-val")
		bag, _ := baggage.New(m)
		ctx = baggage.ContextWithBaggage(ctx, bag)

		p.Middleware()(inner).ServeHTTP(w, r.WithContext(ctx))
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Upstream entry preserved
	if m := gotBaggage.Member("upstream-key"); m.Value() != "upstream-val" {
		t.Errorf("expected upstream baggage preserved, got %q", m.Value())
	}
	// Gateway entry added
	if m := gotBaggage.Member("gateway-tag"); m.Value() != "gw" {
		t.Errorf("expected gateway baggage added, got %q", m.Value())
	}
}

func TestPropagator_W3COnlyTag(t *testing.T) {
	// Tag with no header, only W3C baggage key
	cfg := config.BaggageConfig{
		Enabled:    true,
		W3CBaggage: true,
		Tags: []config.BaggageTagDef{
			{Name: "w3c-only", Source: "static:value"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBaggage baggage.Baggage
	var gotHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBaggage = baggage.FromContext(r.Context())
		gotHeader = r.Header.Get("w3c-only")
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should be in W3C baggage
	if m := gotBaggage.Member("w3c-only"); m.Value() != "value" {
		t.Errorf("expected w3c baggage w3c-only=value, got %q", m.Value())
	}
	// Should NOT set a custom header (header field is empty)
	if gotHeader != "" {
		t.Errorf("expected no custom header, got %q", gotHeader)
	}
}

func TestPropagator_Combined(t *testing.T) {
	cfg := config.BaggageConfig{
		Enabled:        true,
		PropagateTrace: true,
		W3CBaggage:     true,
		Tags: []config.BaggageTagDef{
			{Name: "tenant", Source: "header:X-Tenant", Header: "X-Backend-Tenant", BaggageKey: "tenant.id"},
			{Name: "env", Source: "static:prod", Header: "X-Env"},
		},
	}

	p, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var gotBaggage baggage.Baggage
	var gotPropagateTrace bool
	var gotTenantHeader, gotEnvHeader string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBaggage = baggage.FromContext(r.Context())
		vc := variables.GetFromRequest(r)
		gotPropagateTrace = vc.PropagateTrace
		gotTenantHeader = r.Header.Get("X-Backend-Tenant")
		gotEnvHeader = r.Header.Get("X-Env")
	})

	handler := setupVarContext(p.Middleware()(inner))

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Tenant", "acme")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !gotPropagateTrace {
		t.Error("expected PropagateTrace=true")
	}
	if gotTenantHeader != "acme" {
		t.Errorf("expected X-Backend-Tenant=acme, got %s", gotTenantHeader)
	}
	if gotEnvHeader != "prod" {
		t.Errorf("expected X-Env=prod, got %s", gotEnvHeader)
	}
	if m := gotBaggage.Member("tenant.id"); m.Value() != "acme" {
		t.Errorf("expected baggage tenant.id=acme, got %q", m.Value())
	}
	if m := gotBaggage.Member("env"); m.Value() != "prod" {
		t.Errorf("expected baggage env=prod, got %q", m.Value())
	}
}

func TestMergeBaggageConfig(t *testing.T) {
	global := config.BaggageConfig{
		Enabled:        true,
		PropagateTrace: true,
		W3CBaggage:     true,
		Tags: []config.BaggageTagDef{
			{Name: "global-tag", Source: "static:global", Header: "X-Global"},
		},
	}

	t.Run("per-route tags replaces global", func(t *testing.T) {
		perRoute := config.BaggageConfig{
			Enabled: true,
			Tags: []config.BaggageTagDef{
				{Name: "route-tag", Source: "static:route", Header: "X-Route"},
			},
		}
		merged := MergeBaggageConfig(perRoute, global)
		if len(merged.Tags) != 1 || merged.Tags[0].Name != "route-tag" {
			t.Errorf("expected per-route tags to replace global, got %+v", merged.Tags)
		}
		// Bool overlay always wins in MergeNonZero — per-route false overrides global true
	})

	t.Run("per-route explicitly enables bools", func(t *testing.T) {
		perRoute := config.BaggageConfig{
			Enabled:        true,
			PropagateTrace: true,
			W3CBaggage:     true,
			Tags: []config.BaggageTagDef{
				{Name: "route-tag", Source: "static:route", Header: "X-Route"},
			},
		}
		merged := MergeBaggageConfig(perRoute, global)
		if !merged.PropagateTrace {
			t.Error("expected PropagateTrace=true from per-route")
		}
		if !merged.W3CBaggage {
			t.Error("expected W3CBaggage=true from per-route")
		}
	})

	t.Run("per-route overrides bool fields", func(t *testing.T) {
		perRoute := config.BaggageConfig{
			Enabled:        true,
			PropagateTrace: true, // explicitly set
			Tags: []config.BaggageTagDef{
				{Name: "tag", Source: "static:val", Header: "X-Tag"},
			},
		}
		merged := MergeBaggageConfig(perRoute, global)
		if !merged.PropagateTrace {
			t.Error("expected PropagateTrace=true from per-route")
		}
	})

	t.Run("empty per-route inherits global tags", func(t *testing.T) {
		perRoute := config.BaggageConfig{
			Enabled: true,
		}
		merged := MergeBaggageConfig(perRoute, global)
		if len(merged.Tags) != 1 || merged.Tags[0].Name != "global-tag" {
			t.Errorf("expected global tags inherited, got %+v", merged.Tags)
		}
	})
}

func TestStatsIncludeNewFields(t *testing.T) {
	b := NewBaggageByRoute()
	err := b.AddRoute("route1", config.BaggageConfig{
		Enabled:        true,
		PropagateTrace: true,
		W3CBaggage:     true,
		Tags: []config.BaggageTagDef{
			{Name: "env", Source: "static:test", Header: "X-Env"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	stats := b.Stats()
	m := stats["route1"].(map[string]interface{})

	if pt, ok := m["propagate_trace"].(bool); !ok || !pt {
		t.Errorf("expected propagate_trace=true, got %v", m["propagate_trace"])
	}
	if w3c, ok := m["w3c_baggage"].(bool); !ok || !w3c {
		t.Errorf("expected w3c_baggage=true, got %v", m["w3c_baggage"])
	}
}
