package extract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/variables"
)

func TestBuild_Header(t *testing.T) {
	fn := Build("header:X-Tenant")
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tenant", "acme")
	if got := fn(req); got != "acme" {
		t.Errorf("expected acme, got %s", got)
	}
}

func TestBuild_HeaderMissing(t *testing.T) {
	fn := Build("header:X-Missing")
	req := httptest.NewRequest("GET", "/", nil)
	if got := fn(req); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestBuild_JWTClaim(t *testing.T) {
	fn := Build("jwt_claim:sub")
	req := httptest.NewRequest("GET", "/", nil)
	vc := variables.NewContext(req)
	vc.Identity = &variables.Identity{
		Claims: map[string]interface{}{"sub": "user-42"},
	}
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, vc)
	req = req.WithContext(ctx)

	if got := fn(req); got != "user-42" {
		t.Errorf("expected user-42, got %s", got)
	}
}

func TestBuild_JWTClaimMissing(t *testing.T) {
	fn := Build("jwt_claim:sub")
	req := httptest.NewRequest("GET", "/", nil)
	vc := variables.NewContext(req)
	ctx := context.WithValue(req.Context(), variables.RequestContextKey{}, vc)
	req = req.WithContext(ctx)

	if got := fn(req); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestBuild_Query(t *testing.T) {
	fn := Build("query:region")
	req := httptest.NewRequest("GET", "/test?region=us-east-1", nil)
	if got := fn(req); got != "us-east-1" {
		t.Errorf("expected us-east-1, got %s", got)
	}
}

func TestBuild_QueryMissing(t *testing.T) {
	fn := Build("query:missing")
	req := httptest.NewRequest("GET", "/test", nil)
	if got := fn(req); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestBuild_Cookie(t *testing.T) {
	fn := Build("cookie:sid")
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "sid", Value: "abc123"})
	if got := fn(req); got != "abc123" {
		t.Errorf("expected abc123, got %s", got)
	}
}

func TestBuild_CookieMissing(t *testing.T) {
	fn := Build("cookie:missing")
	req := httptest.NewRequest("GET", "/", nil)
	if got := fn(req); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}

func TestBuild_Static(t *testing.T) {
	fn := Build("static:production")
	req := httptest.NewRequest("GET", "/", nil)
	if got := fn(req); got != "production" {
		t.Errorf("expected production, got %s", got)
	}
}

func TestBuild_Unknown(t *testing.T) {
	fn := Build("unknown:foo")
	req := httptest.NewRequest("GET", "/", nil)
	if got := fn(req); got != "" {
		t.Errorf("expected empty, got %s", got)
	}
}
