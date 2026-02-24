package baggage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
}
