package nonce

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/variables"
)

func TestMemoryStore_BasicCheckAndStore(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	ctx := context.Background()

	// First check should succeed (new nonce)
	ok, err := store.CheckAndStore(ctx, "nonce1", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected first check to return true (new)")
	}

	// Second check with same nonce should fail (duplicate)
	ok, err = store.CheckAndStore(ctx, "nonce1", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected second check to return false (duplicate)")
	}

	// Different nonce should succeed
	ok, err = store.CheckAndStore(ctx, "nonce2", 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected different nonce to return true (new)")
	}
}

func TestMemoryStore_TTLExpiration(t *testing.T) {
	store := NewMemoryStore(50 * time.Millisecond)
	defer store.Close()

	ctx := context.Background()

	ok, _ := store.CheckAndStore(ctx, "nonce1", 50*time.Millisecond)
	if !ok {
		t.Fatal("expected first check to succeed")
	}

	// Wait for expiration
	time.Sleep(100 * time.Millisecond)

	// Should succeed again after expiry
	ok, _ = store.CheckAndStore(ctx, "nonce1", 50*time.Millisecond)
	if !ok {
		t.Fatal("expected check to succeed after TTL expiration")
	}
}

func TestMemoryStore_Size(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	ctx := context.Background()

	if store.Size() != 0 {
		t.Fatalf("expected size 0, got %d", store.Size())
	}

	store.CheckAndStore(ctx, "a", 5*time.Minute)
	store.CheckAndStore(ctx, "b", 5*time.Minute)

	if store.Size() != 2 {
		t.Fatalf("expected size 2, got %d", store.Size())
	}
}

func TestMemoryStore_ConcurrentAccess(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	successes := make(chan bool, 100)

	// Send the same nonce from 100 goroutines — exactly one should succeed
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _ := store.CheckAndStore(ctx, "same-nonce", 5*time.Minute)
			successes <- ok
		}()
	}

	wg.Wait()
	close(successes)

	count := 0
	for ok := range successes {
		if ok {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 success for concurrent same nonce, got %d", count)
	}
}

func TestMemoryStore_Cleanup(t *testing.T) {
	store := NewMemoryStore(50 * time.Millisecond)
	defer store.Close()

	ctx := context.Background()
	store.CheckAndStore(ctx, "expires", 50*time.Millisecond)

	if store.Size() != 1 {
		t.Fatalf("expected size 1, got %d", store.Size())
	}

	// Wait for cleanup (cleanup runs at ttl/2 = 25ms, min 1s — but we use 50ms TTL so cleanup at 1s)
	// Instead manually trigger by waiting for expiry and then inserting (cleanup is background)
	time.Sleep(150 * time.Millisecond)

	// Force a cleanup check by waiting for the ticker interval (minimum 1s)
	// For a faster test, just verify the expired entry is treated as new
	ok, _ := store.CheckAndStore(ctx, "expires", 50*time.Millisecond)
	if !ok {
		t.Fatal("expected expired entry to be treated as new")
	}
}

func TestNonceChecker_MissingHeader_Required(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:  true,
		Required: true,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
	}, store)

	r := httptest.NewRequest("GET", "/test", nil)
	addVarContext(r)

	allowed, status, msg := nc.Check(r)
	if allowed {
		t.Fatal("expected request without nonce to be rejected")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
	if msg != "missing nonce" {
		t.Fatalf("expected 'missing nonce', got %q", msg)
	}
}

func TestNonceChecker_MissingHeader_NotRequired(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:  true,
		Required: false,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
	}, store)

	r := httptest.NewRequest("GET", "/test", nil)
	addVarContext(r)

	allowed, status, _ := nc.Check(r)
	if !allowed {
		t.Fatal("expected request without nonce to be allowed when not required")
	}
	if status != 0 {
		t.Fatalf("expected status 0, got %d", status)
	}
}

func TestNonceChecker_DuplicateNonce(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:  true,
		Required: true,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
	}, store)

	// First request
	r1 := httptest.NewRequest("GET", "/test", nil)
	r1.Header.Set("X-Nonce", "abc123")
	addVarContext(r1)

	allowed, _, _ := nc.Check(r1)
	if !allowed {
		t.Fatal("expected first request to be allowed")
	}

	// Replay
	r2 := httptest.NewRequest("GET", "/test", nil)
	r2.Header.Set("X-Nonce", "abc123")
	addVarContext(r2)

	allowed, status, msg := nc.Check(r2)
	if allowed {
		t.Fatal("expected replay to be rejected")
	}
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
	if msg != "replay detected" {
		t.Fatalf("expected 'replay detected', got %q", msg)
	}
}

func TestNonceChecker_UniqueNonces(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:  true,
		Required: true,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
	}, store)

	for i := 0; i < 10; i++ {
		r := httptest.NewRequest("GET", "/test", nil)
		r.Header.Set("X-Nonce", "nonce-"+strconv.Itoa(i))
		addVarContext(r)

		allowed, _, _ := nc.Check(r)
		if !allowed {
			t.Fatalf("expected unique nonce %d to be allowed", i)
		}
	}
}

func TestNonceChecker_PerClientScope(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:  true,
		Required: true,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
		Scope:    "per_client",
	}, store)

	// Client A sends nonce "abc"
	r1 := httptest.NewRequest("GET", "/test", nil)
	r1.Header.Set("X-Nonce", "abc")
	r1.RemoteAddr = "1.2.3.4:1234"
	addVarContextWithIdentity(r1, "client-a")

	allowed, _, _ := nc.Check(r1)
	if !allowed {
		t.Fatal("expected client-a nonce to be allowed")
	}

	// Client B sends same nonce "abc" — should be allowed (different client)
	r2 := httptest.NewRequest("GET", "/test", nil)
	r2.Header.Set("X-Nonce", "abc")
	r2.RemoteAddr = "5.6.7.8:5678"
	addVarContextWithIdentity(r2, "client-b")

	allowed, _, _ = nc.Check(r2)
	if !allowed {
		t.Fatal("expected same nonce from different client to be allowed in per_client scope")
	}

	// Client A sends same nonce "abc" again — should be rejected
	r3 := httptest.NewRequest("GET", "/test", nil)
	r3.Header.Set("X-Nonce", "abc")
	r3.RemoteAddr = "1.2.3.4:1234"
	addVarContextWithIdentity(r3, "client-a")

	allowed, status, _ := nc.Check(r3)
	if allowed {
		t.Fatal("expected replay from same client to be rejected")
	}
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
}

func TestNonceChecker_GlobalScope(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:  true,
		Required: true,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
		Scope:    "global",
	}, store)

	// Client A sends nonce "abc"
	r1 := httptest.NewRequest("GET", "/test", nil)
	r1.Header.Set("X-Nonce", "abc")
	addVarContextWithIdentity(r1, "client-a")

	allowed, _, _ := nc.Check(r1)
	if !allowed {
		t.Fatal("expected first use to be allowed")
	}

	// Client B sends same nonce "abc" — should be rejected (global scope)
	r2 := httptest.NewRequest("GET", "/test", nil)
	r2.Header.Set("X-Nonce", "abc")
	addVarContextWithIdentity(r2, "client-b")

	allowed, status, _ := nc.Check(r2)
	if allowed {
		t.Fatal("expected same nonce from different client to be rejected in global scope")
	}
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
}

func TestNonceChecker_TimestampValidation(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:         true,
		Required:        true,
		Header:          "X-Nonce",
		TTL:             5 * time.Minute,
		TimestampHeader: "X-Timestamp",
		MaxAge:          10 * time.Second,
	}, store)

	// Valid timestamp
	r1 := httptest.NewRequest("GET", "/test", nil)
	r1.Header.Set("X-Nonce", "nonce1")
	r1.Header.Set("X-Timestamp", time.Now().Format(time.RFC3339))
	addVarContext(r1)

	allowed, _, _ := nc.Check(r1)
	if !allowed {
		t.Fatal("expected request with valid timestamp to be allowed")
	}

	// Stale timestamp
	r2 := httptest.NewRequest("GET", "/test", nil)
	r2.Header.Set("X-Nonce", "nonce2")
	r2.Header.Set("X-Timestamp", time.Now().Add(-1*time.Minute).Format(time.RFC3339))
	addVarContext(r2)

	allowed, status, msg := nc.Check(r2)
	if allowed {
		t.Fatal("expected stale timestamp to be rejected")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
	if msg != "request too old" {
		t.Fatalf("expected 'request too old', got %q", msg)
	}

	// Unix timestamp
	r3 := httptest.NewRequest("GET", "/test", nil)
	r3.Header.Set("X-Nonce", "nonce3")
	r3.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	addVarContext(r3)

	allowed, _, _ = nc.Check(r3)
	if !allowed {
		t.Fatal("expected request with unix timestamp to be allowed")
	}

	// Invalid timestamp format
	r4 := httptest.NewRequest("GET", "/test", nil)
	r4.Header.Set("X-Nonce", "nonce4")
	r4.Header.Set("X-Timestamp", "not-a-timestamp")
	addVarContext(r4)

	allowed, status, msg = nc.Check(r4)
	if allowed {
		t.Fatal("expected invalid timestamp to be rejected")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
	if msg != "invalid timestamp" {
		t.Fatalf("expected 'invalid timestamp', got %q", msg)
	}
}

func TestNonceChecker_QueryParam(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:    true,
		Required:   true,
		Header:     "X-Nonce",
		QueryParam: "nonce",
		TTL:        5 * time.Minute,
	}, store)

	// Nonce in query param (no header)
	r1 := httptest.NewRequest("GET", "/test?nonce=query-abc", nil)
	addVarContext(r1)

	allowed, _, _ := nc.Check(r1)
	if !allowed {
		t.Fatal("expected nonce from query param to be allowed")
	}

	// Same nonce in query param — replay
	r2 := httptest.NewRequest("GET", "/test?nonce=query-abc", nil)
	addVarContext(r2)

	allowed, status, msg := nc.Check(r2)
	if allowed {
		t.Fatal("expected duplicate query param nonce to be rejected")
	}
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}
	if msg != "replay detected" {
		t.Fatalf("expected 'replay detected', got %q", msg)
	}
}

func TestNonceChecker_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	nc := New("test-route", config.NonceConfig{
		Enabled:    true,
		Required:   true,
		Header:     "X-Nonce",
		QueryParam: "nonce",
		TTL:        5 * time.Minute,
	}, store)

	// Both header and query — header takes precedence
	r1 := httptest.NewRequest("GET", "/test?nonce=query-val", nil)
	r1.Header.Set("X-Nonce", "header-val")
	addVarContext(r1)

	allowed, _, _ := nc.Check(r1)
	if !allowed {
		t.Fatal("expected request to be allowed")
	}

	// Replay using same header value — should be rejected
	r2 := httptest.NewRequest("GET", "/test?nonce=different-query", nil)
	r2.Header.Set("X-Nonce", "header-val")
	addVarContext(r2)

	allowed, status, _ := nc.Check(r2)
	if allowed {
		t.Fatal("expected duplicate header nonce to be rejected even with different query")
	}
	if status != http.StatusConflict {
		t.Fatalf("expected 409, got %d", status)
	}

	// query-val was never consumed — should still be new
	r3 := httptest.NewRequest("GET", "/test?nonce=query-val", nil)
	addVarContext(r3)

	allowed, _, _ = nc.Check(r3)
	if !allowed {
		t.Fatal("expected query-val nonce to be allowed (header took precedence earlier)")
	}
}

func TestNonceChecker_QueryParamMissing_Required(t *testing.T) {
	store := NewMemoryStore(5 * time.Minute)
	defer store.Close()

	// No header configured, only query param
	nc := New("test-route", config.NonceConfig{
		Enabled:    true,
		Required:   true,
		QueryParam: "nonce",
		TTL:        5 * time.Minute,
	}, store)

	// Request with no header and no query param
	r := httptest.NewRequest("GET", "/test", nil)
	addVarContext(r)

	allowed, status, msg := nc.Check(r)
	if allowed {
		t.Fatal("expected missing nonce to be rejected")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", status)
	}
	if msg != "missing nonce" {
		t.Fatalf("expected 'missing nonce', got %q", msg)
	}
}

func TestMergeNonceConfig(t *testing.T) {
	global := config.NonceConfig{
		Enabled:    true,
		Header:     "X-Nonce",
		QueryParam: "nonce",
		TTL:        5 * time.Minute,
		Mode:       "local",
		Scope:      "global",
		Required:   true,
	}

	perRoute := config.NonceConfig{
		Enabled:    true,
		TTL:        10 * time.Minute,
		Scope:      "per_client",
		QueryParam: "idempotency_key",
	}

	merged := MergeNonceConfig(perRoute, global)

	if merged.Header != "X-Nonce" {
		t.Fatalf("expected header 'X-Nonce', got %q", merged.Header)
	}
	if merged.QueryParam != "idempotency_key" {
		t.Fatalf("expected query_param 'idempotency_key', got %q", merged.QueryParam)
	}
	if merged.TTL != 10*time.Minute {
		t.Fatalf("expected TTL 10m, got %v", merged.TTL)
	}
	if merged.Scope != "per_client" {
		t.Fatalf("expected scope 'per_client', got %q", merged.Scope)
	}
	if merged.Mode != "local" {
		t.Fatalf("expected mode 'local', got %q", merged.Mode)
	}
	if !merged.Enabled {
		t.Fatal("expected enabled to be true")
	}
}

func TestNonceByRoute_AddAndGet(t *testing.T) {
	m := NewNonceByRoute(nil)
	defer byroute.ForEach(&m.Manager, func(nc *NonceChecker) { nc.CloseStore() })

	err := m.AddRoute("route-1", config.NonceConfig{
		Enabled:  true,
		Required: true,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	nc := m.Lookup("route-1")
	if nc == nil {
		t.Fatal("expected checker for route-1")
	}

	nc2 := m.Lookup("route-nonexistent")
	if nc2 != nil {
		t.Fatal("expected nil for nonexistent route")
	}
}

func TestNonceByRoute_Stats(t *testing.T) {
	m := NewNonceByRoute(nil)
	defer byroute.ForEach(&m.Manager, func(nc *NonceChecker) { nc.CloseStore() })

	m.AddRoute("route-1", config.NonceConfig{
		Enabled:  true,
		Required: true,
		Header:   "X-Nonce",
		TTL:      5 * time.Minute,
	})

	stats := m.Stats()
	if len(stats) != 1 {
		t.Fatalf("expected 1 stat entry, got %d", len(stats))
	}
	if _, ok := stats["route-1"]; !ok {
		t.Fatal("expected stats for route-1")
	}
}

func TestNonceByRoute_RouteIDs(t *testing.T) {
	m := NewNonceByRoute(nil)
	defer byroute.ForEach(&m.Manager, func(nc *NonceChecker) { nc.CloseStore() })

	m.AddRoute("a", config.NonceConfig{Enabled: true, Required: true, TTL: time.Minute})
	m.AddRoute("b", config.NonceConfig{Enabled: true, Required: true, TTL: time.Minute})

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 route IDs, got %d", len(ids))
	}
}

// helpers

func addVarContext(r *http.Request) {
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, variables.NewContext(r))
	*r = *r.WithContext(ctx)
}

func addVarContextWithIdentity(r *http.Request, clientID string) {
	varCtx := variables.NewContext(r)
	varCtx.Identity = &variables.Identity{ClientID: clientID}
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	*r = *r.WithContext(ctx)
}
