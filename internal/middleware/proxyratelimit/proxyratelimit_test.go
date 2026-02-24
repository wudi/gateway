package proxyratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestProxyLimiter_AllowsUpToRate(t *testing.T) {
	pl := New(config.ProxyRateLimitConfig{
		Enabled: true,
		Rate:    5,
		Period:  time.Second,
		Burst:   5,
	})

	handler := pl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	allowed := 0
	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		handler.ServeHTTP(rec, req)
		if rec.Code == 200 {
			allowed++
		}
	}

	if allowed != 5 {
		t.Errorf("expected 5 allowed, got %d", allowed)
	}

	stats := pl.Stats()
	if stats["allowed"] != 5 {
		t.Errorf("expected 5 allowed in stats, got %d", stats["allowed"])
	}
	if stats["rejected"] != 5 {
		t.Errorf("expected 5 rejected in stats, got %d", stats["rejected"])
	}
}

func TestProxyLimiter_Returns503WithRetryAfter(t *testing.T) {
	pl := New(config.ProxyRateLimitConfig{
		Enabled: true,
		Rate:    1,
		Period:  time.Minute,
		Burst:   1,
	})

	handler := pl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// First request succeeds
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Second request rejected
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
}

func TestProxyLimiter_DefaultPeriod(t *testing.T) {
	pl := New(config.ProxyRateLimitConfig{
		Enabled: true,
		Rate:    100,
	})

	handler := pl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestProxyRateLimitByRoute(t *testing.T) {
	m := NewProxyRateLimitByRoute()
	m.AddRoute("route1", config.ProxyRateLimitConfig{
		Enabled: true,
		Rate:    10,
		Period:  time.Second,
	})

	if m.GetLimiter("route1") == nil {
		t.Error("expected limiter for route1")
	}
	if m.GetLimiter("nonexistent") != nil {
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
