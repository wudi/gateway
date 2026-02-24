package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/variables"
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

// helper to create a request with variables context containing identity
func reqWithIdentity(identity *variables.Identity) *http.Request {
	r := httptest.NewRequest("GET", "/api/test", nil)
	r.RemoteAddr = "10.0.0.1:12345"
	varCtx := variables.NewContext(r)
	varCtx.Identity = identity
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	return r.WithContext(ctx)
}

func TestBuildKeyFunc_IP(t *testing.T) {
	fn := BuildKeyFunc(false, "ip")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:5678"
	got := fn(r)
	if got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %q", got)
	}
}

func TestBuildKeyFunc_PerIPFlag(t *testing.T) {
	fn := BuildKeyFunc(true, "")
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:5678"
	got := fn(r)
	if got != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %q", got)
	}
}

func TestBuildKeyFunc_Header(t *testing.T) {
	fn := BuildKeyFunc(false, "header:X-Tenant-ID")

	// Header present
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:5678"
	r.Header.Set("X-Tenant-ID", "tenant-abc")
	got := fn(r)
	if got != "header:X-Tenant-ID:tenant-abc" {
		t.Errorf("expected header key, got %q", got)
	}

	// Header missing — fallback to IP
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "1.2.3.4:5678"
	got2 := fn(r2)
	if got2 != "1.2.3.4" {
		t.Errorf("expected IP fallback, got %q", got2)
	}
}

func TestBuildKeyFunc_Cookie(t *testing.T) {
	fn := BuildKeyFunc(false, "cookie:session")

	// Cookie present
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "1.2.3.4:5678"
	r.AddCookie(&http.Cookie{Name: "session", Value: "sess-123"})
	got := fn(r)
	if got != "cookie:session:sess-123" {
		t.Errorf("expected cookie key, got %q", got)
	}

	// Cookie missing — fallback to IP
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "1.2.3.4:5678"
	got2 := fn(r2)
	if got2 != "1.2.3.4" {
		t.Errorf("expected IP fallback, got %q", got2)
	}
}

func TestBuildKeyFunc_JWTClaim(t *testing.T) {
	fn := BuildKeyFunc(false, "jwt_claim:sub")

	// Claim present
	r := reqWithIdentity(&variables.Identity{
		ClientID: "client-1",
		AuthType: "jwt",
		Claims:   map[string]interface{}{"sub": "user-42"},
	})
	got := fn(r)
	if got != "jwt_claim:sub:user-42" {
		t.Errorf("expected jwt_claim key, got %q", got)
	}

	// No identity — fallback to IP
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "1.2.3.4:5678"
	got2 := fn(r2)
	if got2 != "1.2.3.4" {
		t.Errorf("expected IP fallback, got %q", got2)
	}

	// Identity present but claim missing — fallback to IP
	r3 := reqWithIdentity(&variables.Identity{
		ClientID: "client-1",
		AuthType: "jwt",
		Claims:   map[string]interface{}{"iss": "auth.example.com"},
	})
	got3 := fn(r3)
	if got3 != "10.0.0.1" {
		t.Errorf("expected IP fallback when claim missing, got %q", got3)
	}
}

func TestBuildKeyFunc_ClientID(t *testing.T) {
	fn := BuildKeyFunc(false, "client_id")

	// Authenticated
	r := reqWithIdentity(&variables.Identity{
		ClientID: "my-client",
		AuthType: "api_key",
	})
	got := fn(r)
	if got != "my-client" {
		t.Errorf("expected my-client, got %q", got)
	}

	// Not authenticated — fallback to IP
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "1.2.3.4:5678"
	got2 := fn(r2)
	if got2 != "1.2.3.4" {
		t.Errorf("expected IP fallback, got %q", got2)
	}
}

func TestBuildKeyFunc_Default(t *testing.T) {
	fn := BuildKeyFunc(false, "")

	// Authenticated — uses client ID
	r := reqWithIdentity(&variables.Identity{
		ClientID: "my-client",
		AuthType: "jwt",
	})
	got := fn(r)
	if got != "my-client" {
		t.Errorf("expected my-client, got %q", got)
	}

	// Not authenticated — fallback to IP
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "1.2.3.4:5678"
	got2 := fn(r2)
	if got2 != "1.2.3.4" {
		t.Errorf("expected IP fallback, got %q", got2)
	}
}

func TestLimiterWithHeaderKey(t *testing.T) {
	limiter := NewLimiter(Config{
		Rate:   2,
		Period: time.Second,
		Burst:  2,
		Key:    "header:X-API-Key",
	})

	handler := limiter.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Two requests from same API key should exhaust the limit
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-API-Key", "key-1")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	// Third request from same API key should be limited
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "key-1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}

	// Different API key should still be allowed
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-API-Key", "key-2")
	rr2 := httptest.NewRecorder()
	handler.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("different key should be allowed, got %d", rr2.Code)
	}
}

func TestTieredLimiter_BasicTiers(t *testing.T) {
	cfg := TieredConfig{
		Tiers: map[string]Config{
			"free": {
				Rate:   2,
				Period: time.Second,
				Burst:  2,
			},
			"premium": {
				Rate:   10,
				Period: time.Second,
				Burst:  10,
			},
		},
		TierKey:     "header:X-Tier",
		DefaultTier: "free",
		KeyFn: func(r *http.Request) string {
			return "global"
		},
	}

	tl := NewTieredLimiter(cfg)
	handler := tl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Free tier: should allow 2 then reject
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Tier", "free")
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("free tier request %d: expected 200, got %d", i, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tier", "free")
	handler.ServeHTTP(rec, req)
	if rec.Code != 429 {
		t.Errorf("free tier request 3: expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("X-RateLimit-Tier") != "free" {
		t.Errorf("expected X-RateLimit-Tier=free, got %s", rec.Header().Get("X-RateLimit-Tier"))
	}

	// Premium tier: should still have capacity
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Tier", "premium")
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("premium tier request: expected 200, got %d", rec.Code)
	}
}

func TestTieredLimiter_DefaultTierFallback(t *testing.T) {
	cfg := TieredConfig{
		Tiers: map[string]Config{
			"default": {
				Rate:   5,
				Period: time.Second,
				Burst:  5,
			},
		},
		TierKey:     "header:X-Tier",
		DefaultTier: "default",
	}

	tl := NewTieredLimiter(cfg)
	handler := tl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// No X-Tier header → falls back to default
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("expected 200 for default tier, got %d", rec.Code)
	}
}

func TestTieredLimiter_PerClientKey(t *testing.T) {
	cfg := TieredConfig{
		Tiers: map[string]Config{
			"basic": {
				Rate:   1,
				Period: time.Second,
				Burst:  1,
			},
		},
		TierKey:     "header:X-Tier",
		DefaultTier: "basic",
		KeyFn: func(r *http.Request) string {
			return r.Header.Get("X-Client")
		},
	}

	tl := NewTieredLimiter(cfg)
	handler := tl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Client A uses their quota
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Client", "clientA")
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("clientA first: expected 200, got %d", rec.Code)
	}

	// Client A is limited
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Client", "clientA")
	handler.ServeHTTP(rec, req)
	if rec.Code != 429 {
		t.Errorf("clientA second: expected 429, got %d", rec.Code)
	}

	// Client B still has quota (separate key)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Client", "clientB")
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("clientB first: expected 200, got %d", rec.Code)
	}
}

func TestRateLimitByRoute_Tiered(t *testing.T) {
	rl := NewRateLimitByRoute()
	rl.AddRouteTiered("route1", TieredConfig{
		Tiers: map[string]Config{
			"basic": {Rate: 5, Period: time.Second, Burst: 5},
		},
		TierKey:     "header:X-Tier",
		DefaultTier: "basic",
	})

	if rl.GetTieredLimiter("route1") == nil {
		t.Error("expected tiered limiter for route1")
	}
	if rl.GetMiddleware("route1") == nil {
		t.Error("expected middleware for route1")
	}

	ids := rl.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
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

func BenchmarkTokenBucketAllowParallel(b *testing.B) {
	cfg := Config{
		Rate:   1000000,
		Period: time.Second,
		Burst:  1000000,
	}
	tb := NewTokenBucket(cfg)

	// Pre-build keys to avoid measuring fmt.Sprintf in the hot loop
	keys := make([]string, 256)
	for i := range keys {
		keys[i] = fmt.Sprintf("ip-%d", i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			tb.Allow(keys[i%256])
			i++
		}
	})
}
