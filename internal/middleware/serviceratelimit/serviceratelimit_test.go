package serviceratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestServiceLimiter_AllowsWithinRate(t *testing.T) {
	sl := New(config.ServiceRateLimitConfig{
		Enabled: true,
		Rate:    100,
		Period:  time.Second,
	})

	handler := sl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 50; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	stats := sl.Stats()
	if stats["allowed"].(int64) != 50 {
		t.Errorf("expected 50 allowed, got %v", stats["allowed"])
	}
}

func TestServiceLimiter_RejectsOverRate(t *testing.T) {
	sl := New(config.ServiceRateLimitConfig{
		Enabled: true,
		Rate:    5,
		Period:  time.Second,
		Burst:   5,
	})

	handler := sl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowed := 0
	rejected := 0
	for i := 0; i < 20; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			allowed++
		} else if rr.Code == http.StatusTooManyRequests {
			rejected++
		}
	}

	if allowed != 5 {
		t.Errorf("expected 5 allowed, got %d", allowed)
	}
	if rejected != 15 {
		t.Errorf("expected 15 rejected, got %d", rejected)
	}

	stats := sl.Stats()
	if stats["rejected"].(int64) != 15 {
		t.Errorf("expected 15 rejected in stats, got %v", stats["rejected"])
	}
}

func TestServiceLimiter_RetryAfterHeader(t *testing.T) {
	sl := New(config.ServiceRateLimitConfig{
		Enabled: true,
		Rate:    1,
		Period:  time.Second,
		Burst:   1,
	})

	handler := sl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request allowed
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("first request should succeed")
	}

	// Second request rejected
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second request should be rejected")
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on rejected response")
	}
}

func TestServiceLimiter_DefaultBurst(t *testing.T) {
	sl := New(config.ServiceRateLimitConfig{
		Enabled: true,
		Rate:    10,
		Period:  time.Second,
		// Burst defaults to Rate=10
	})

	handler := sl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// All 10 burst requests should succeed
	for i := 0; i < 10; i++ {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200 within burst, got %d", i, rr.Code)
		}
	}

	// 11th should be rejected
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("11th request should be rejected, got %d", rr.Code)
	}
}
