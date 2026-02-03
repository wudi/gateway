package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenBucket(t *testing.T) {
	cfg := Config{
		Rate:   10, // 10 requests per second
		Period: time.Second,
		Burst:  10,
	}

	tb := NewTokenBucket(cfg)

	// Should allow initial burst
	for i := 0; i < 10; i++ {
		allowed, remaining, _ := tb.Allow("test-key")
		if !allowed {
			t.Errorf("request %d should be allowed", i)
		}
		if remaining != 10-i-1 {
			t.Errorf("expected remaining %d, got %d", 10-i-1, remaining)
		}
	}

	// 11th request should be denied
	allowed, _, _ := tb.Allow("test-key")
	if allowed {
		t.Error("11th request should be denied")
	}
}

func TestTokenBucketRefill(t *testing.T) {
	cfg := Config{
		Rate:   100, // 100 requests per second
		Period: time.Second,
		Burst:  10,
	}

	tb := NewTokenBucket(cfg)

	// Use all tokens
	for i := 0; i < 10; i++ {
		tb.Allow("test-key")
	}

	// Wait for some refill
	time.Sleep(200 * time.Millisecond)

	// Should have some tokens now
	allowed, _, _ := tb.Allow("test-key")
	if !allowed {
		t.Error("should have refilled some tokens")
	}
}

func TestTokenBucketMultipleKeys(t *testing.T) {
	cfg := Config{
		Rate:   10,
		Period: time.Second,
		Burst:  5,
	}

	tb := NewTokenBucket(cfg)

	// Use all tokens for key1
	for i := 0; i < 5; i++ {
		tb.Allow("key1")
	}

	// key2 should still have tokens
	allowed, _, _ := tb.Allow("key2")
	if !allowed {
		t.Error("key2 should have tokens")
	}

	// key1 should be exhausted
	allowed, _, _ = tb.Allow("key1")
	if allowed {
		t.Error("key1 should be exhausted")
	}
}

func TestLimiterMiddleware(t *testing.T) {
	cfg := Config{
		Rate:   5,
		Period: time.Second,
		Burst:  5,
		PerIP:  true,
	}

	limiter := NewLimiter(cfg)

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 5 requests should succeed
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rr.Code)
		}

		// Check rate limit headers
		if rr.Header().Get("X-RateLimit-Limit") == "" {
			t.Error("missing X-RateLimit-Limit header")
		}
	}

	// 6th request should be rate limited
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}

	// Check Retry-After header
	if rr.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}
}

func TestLimiterDifferentIPs(t *testing.T) {
	cfg := Config{
		Rate:   2,
		Period: time.Second,
		Burst:  2,
		PerIP:  true,
	}

	limiter := NewLimiter(cfg)

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Use all tokens for IP 1
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/api/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	// IP 1 should be rate limited
	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("IP 1 should be rate limited, got %d", rr.Code)
	}

	// IP 2 should still be allowed
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "192.168.1.2:12345"
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("IP 2 should be allowed, got %d", rr.Code)
	}
}

func TestRateLimitByRoute(t *testing.T) {
	rl := NewRateLimitByRoute()

	rl.AddRoute("route1", Config{
		Rate:   5,
		Period: time.Second,
		Burst:  5,
	})

	rl.AddRoute("route2", Config{
		Rate:   10,
		Period: time.Second,
		Burst:  10,
	})

	limiter1 := rl.GetLimiter("route1")
	limiter2 := rl.GetLimiter("route2")

	if limiter1 == nil {
		t.Error("expected limiter for route1")
	}

	if limiter2 == nil {
		t.Error("expected limiter for route2")
	}

	if rl.GetLimiter("unknown") != nil {
		t.Error("expected nil for unknown route")
	}
}

func BenchmarkTokenBucketAllow(b *testing.B) {
	cfg := Config{
		Rate:   1000000, // Very high rate to avoid blocking
		Period: time.Second,
		Burst:  1000000,
	}

	tb := NewTokenBucket(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.Allow("test-key")
	}
}
