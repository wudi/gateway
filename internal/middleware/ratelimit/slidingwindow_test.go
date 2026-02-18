package ratelimit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlidingWindowCounter_BasicAllowDeny(t *testing.T) {
	cfg := Config{
		Rate:   5,
		Period: time.Second,
	}

	sw := NewSlidingWindowCounter(cfg)

	// Should allow first 5 requests
	for i := 0; i < 5; i++ {
		allowed, remaining, _ := sw.Allow("test-key")
		if !allowed {
			t.Errorf("request %d should be allowed", i)
		}
		expected := 5 - i - 1
		if remaining != expected {
			t.Errorf("request %d: expected remaining %d, got %d", i, expected, remaining)
		}
	}

	// 6th request should be denied
	allowed, remaining, _ := sw.Allow("test-key")
	if allowed {
		t.Error("6th request should be denied")
	}
	if remaining != 0 {
		t.Errorf("expected remaining 0, got %d", remaining)
	}
}

func TestSlidingWindowCounter_WindowRotation(t *testing.T) {
	cfg := Config{
		Rate:   10,
		Period: 100 * time.Millisecond,
	}

	sw := NewSlidingWindowCounter(cfg)

	// Use all 10 requests in the first window
	for i := 0; i < 10; i++ {
		allowed, _, _ := sw.Allow("test-key")
		if !allowed {
			t.Errorf("request %d should be allowed", i)
		}
	}

	// Should be denied now
	allowed, _, _ := sw.Allow("test-key")
	if allowed {
		t.Error("should be denied after using all requests")
	}

	// Wait for window to rotate (prev count will still influence)
	time.Sleep(110 * time.Millisecond)

	// After one period, prev=10, curr=0
	// weight ≈ 1.0 (just entered new window), estimate ≈ 10 * 1.0 = 10
	// At the very start of the new window, we'd still be limited.
	// Wait a bit more so the weight drops enough
	time.Sleep(60 * time.Millisecond)

	// Now weight should be about 0.4, estimate ≈ 10 * 0.4 = 4, which is < 10
	allowed, _, _ = sw.Allow("test-key")
	if !allowed {
		t.Error("should be allowed after window rotation with reduced weight")
	}
}

func TestSlidingWindowCounter_FullRecovery(t *testing.T) {
	cfg := Config{
		Rate:   5,
		Period: 50 * time.Millisecond,
	}

	sw := NewSlidingWindowCounter(cfg)

	// Use all requests
	for i := 0; i < 5; i++ {
		sw.Allow("test-key")
	}

	// Wait for 2 full periods so prev window has zero impact
	time.Sleep(110 * time.Millisecond)

	// Should be fully recovered
	for i := 0; i < 5; i++ {
		allowed, _, _ := sw.Allow("test-key")
		if !allowed {
			t.Errorf("request %d should be allowed after full recovery", i)
		}
	}
}

func TestSlidingWindowCounter_MultipleKeys(t *testing.T) {
	cfg := Config{
		Rate:   3,
		Period: time.Second,
	}

	sw := NewSlidingWindowCounter(cfg)

	// Exhaust key1
	for i := 0; i < 3; i++ {
		sw.Allow("key1")
	}

	// key1 should be denied
	allowed, _, _ := sw.Allow("key1")
	if allowed {
		t.Error("key1 should be denied")
	}

	// key2 should still be allowed
	allowed, _, _ = sw.Allow("key2")
	if !allowed {
		t.Error("key2 should be allowed")
	}
}

func TestSlidingWindowLimiter_Middleware(t *testing.T) {
	cfg := Config{
		Rate:   5,
		Period: time.Second,
		PerIP:  true,
	}

	limiter := NewSlidingWindowLimiter(cfg)

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

		if rr.Header().Get("X-RateLimit-Limit") == "" {
			t.Error("missing X-RateLimit-Limit header")
		}
		if rr.Header().Get("X-RateLimit-Remaining") == "" {
			t.Error("missing X-RateLimit-Remaining header")
		}
		if rr.Header().Get("X-RateLimit-Reset") == "" {
			t.Error("missing X-RateLimit-Reset header")
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

	if rr.Header().Get("Retry-After") == "" {
		t.Error("missing Retry-After header")
	}
}

func TestSlidingWindowLimiter_PerIP(t *testing.T) {
	cfg := Config{
		Rate:   2,
		Period: time.Second,
		PerIP:  true,
	}

	limiter := NewSlidingWindowLimiter(cfg)

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Exhaust IP 1
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

func TestSlidingWindowLimiter_Allow(t *testing.T) {
	cfg := Config{
		Rate:   2,
		Period: time.Second,
		PerIP:  true,
	}

	limiter := NewSlidingWindowLimiter(cfg)

	req := httptest.NewRequest("GET", "/api/test", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	if !limiter.Allow(req) {
		t.Error("first request should be allowed")
	}
	if !limiter.Allow(req) {
		t.Error("second request should be allowed")
	}
	if limiter.Allow(req) {
		t.Error("third request should be denied")
	}
}

func TestRateLimitByRoute_SlidingWindow(t *testing.T) {
	rl := NewRateLimitByRoute()

	rl.AddRouteSlidingWindow("route1", Config{
		Rate:   5,
		Period: time.Second,
	})

	// Should be retrievable
	sw := rl.GetSlidingWindowLimiter("route1")
	if sw == nil {
		t.Error("expected sliding window limiter for route1")
	}

	// Unknown route should be nil
	if rl.GetSlidingWindowLimiter("unknown") != nil {
		t.Error("expected nil for unknown route")
	}

	// RouteIDs should include the sliding window route
	ids := rl.RouteIDs()
	found := false
	for _, id := range ids {
		if id == "route1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected route1 in RouteIDs()")
	}
}

func TestRateLimitByRoute_GetMiddlewarePriority(t *testing.T) {
	rl := NewRateLimitByRoute()

	// Add sliding window limiter
	rl.AddRouteSlidingWindow("sw-route", Config{
		Rate:   5,
		Period: time.Second,
	})

	// Add token bucket limiter
	rl.AddRoute("tb-route", Config{
		Rate:   5,
		Period: time.Second,
		Burst:  5,
	})

	// GetMiddleware should return non-nil for both
	if mw := rl.GetMiddleware("sw-route"); mw == nil {
		t.Error("expected middleware for sliding window route")
	}
	if mw := rl.GetMiddleware("tb-route"); mw == nil {
		t.Error("expected middleware for token bucket route")
	}

	// Unknown route should return nil
	if mw := rl.GetMiddleware("unknown"); mw != nil {
		t.Error("expected nil middleware for unknown route")
	}
}

func TestRateLimitByRoute_SlidingWindowMiddleware(t *testing.T) {
	rl := NewRateLimitByRoute()
	rl.AddRouteSlidingWindow("api", Config{
		Rate:   3,
		Period: time.Second,
		PerIP:  true,
	})

	mw := rl.GetMiddleware("api")
	if mw == nil {
		t.Fatal("expected non-nil middleware")
	}

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First 3 should pass
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	// 4th should be denied
	req := httptest.NewRequest("GET", "/api", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
}

func TestSlidingWindowCounter_BurstOverridesRate(t *testing.T) {
	cfg := Config{
		Rate:   5,
		Period: time.Second,
		Burst:  10, // burst > rate means use burst as limit
	}

	sw := NewSlidingWindowCounter(cfg)

	// Should allow up to 10 requests (burst overrides)
	for i := 0; i < 10; i++ {
		allowed, _, _ := sw.Allow("test-key")
		if !allowed {
			t.Errorf("request %d should be allowed (burst=10)", i)
		}
	}

	// 11th should be denied
	allowed, _, _ := sw.Allow("test-key")
	if allowed {
		t.Error("11th request should be denied")
	}
}

func BenchmarkSlidingWindowCounterAllow(b *testing.B) {
	cfg := Config{
		Rate:   1000000,
		Period: time.Second,
	}

	sw := NewSlidingWindowCounter(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sw.Allow("test-key")
	}
}

func BenchmarkSlidingWindowCounterAllowParallel(b *testing.B) {
	cfg := Config{
		Rate:   1000000,
		Period: time.Second,
	}
	sw := NewSlidingWindowCounter(cfg)

	keys := make([]string, 256)
	for i := range keys {
		keys[i] = fmt.Sprintf("ip-%d", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			sw.Allow(keys[i%256])
			i++
		}
	})
}
