package idempotency

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/variables"
)

func newTestIdempotency(cfg config.IdempotencyConfig) *CompiledIdempotency {
	ci, _ := New("test-route", cfg, nil)
	return ci
}

func newRequestWithKey(method, key string) *http.Request {
	r := httptest.NewRequest(method, "/api/payments", nil)
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	// Set up variable context
	varCtx := &variables.Context{Custom: make(map[string]string)}
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	return r.WithContext(ctx)
}

func TestSkipNonConfiguredMethods(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled: true,
	})
	defer ci.Close()

	r := newRequestWithKey("GET", "key-123")
	outcome := ci.Check(r)
	if outcome.Result != ResultProceed {
		t.Errorf("expected ResultProceed for GET, got %d", outcome.Result)
	}
}

func TestMissingKeyNoEnforce(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled: true,
	})
	defer ci.Close()

	r := newRequestWithKey("POST", "")
	outcome := ci.Check(r)
	if outcome.Result != ResultProceed {
		t.Errorf("expected ResultProceed for missing key without enforce, got %d", outcome.Result)
	}
}

func TestMissingKeyEnforce(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled: true,
		Enforce: true,
	})
	defer ci.Close()

	r := newRequestWithKey("POST", "")
	outcome := ci.Check(r)
	if outcome.Result != ResultReject {
		t.Errorf("expected ResultReject for missing key with enforce, got %d", outcome.Result)
	}
}

func TestKeyTooLong(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled:      true,
		MaxKeyLength: 5,
	})
	defer ci.Close()

	r := newRequestWithKey("POST", "this-key-is-too-long")
	outcome := ci.Check(r)
	if outcome.Result != ResultInvalid {
		t.Errorf("expected ResultInvalid for too-long key, got %d", outcome.Result)
	}
}

func TestNewKeyProceedsAndCachesResponse(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled: true,
	})
	defer ci.Close()

	r := newRequestWithKey("POST", "unique-key-1")
	outcome := ci.Check(r)
	if outcome.Result != ResultProceed {
		t.Fatalf("expected ResultProceed for new key, got %d", outcome.Result)
	}
	if outcome.Key == "" {
		t.Fatal("expected non-empty key")
	}

	// Simulate response
	ci.RecordResponse(outcome.Key, &StoredResponse{
		StatusCode: 201,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"123"}`),
	})

	// Second request with same key should get cached
	r2 := newRequestWithKey("POST", "unique-key-1")
	outcome2 := ci.Check(r2)
	if outcome2.Result != ResultCached {
		t.Fatalf("expected ResultCached for duplicate key, got %d", outcome2.Result)
	}
	if outcome2.Response.StatusCode != 201 {
		t.Errorf("expected status 201, got %d", outcome2.Response.StatusCode)
	}
	if string(outcome2.Response.Body) != `{"id":"123"}` {
		t.Errorf("expected cached body, got %s", string(outcome2.Response.Body))
	}
}

func TestInFlightDeduplication(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled: true,
	})
	defer ci.Close()

	// First request proceeds
	r1 := newRequestWithKey("POST", "inflight-key")
	outcome1 := ci.Check(r1)
	if outcome1.Result != ResultProceed {
		t.Fatalf("expected ResultProceed for first request, got %d", outcome1.Result)
	}

	// Second request should wait
	var wg sync.WaitGroup
	var outcome2 CheckOutcome
	wg.Add(1)
	go func() {
		defer wg.Done()
		r2 := newRequestWithKey("POST", "inflight-key")
		outcome2 = ci.Check(r2)
	}()

	// Allow goroutine to reach wait state
	time.Sleep(50 * time.Millisecond)

	// Complete the first request
	ci.RecordResponse(outcome1.Key, &StoredResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("ok"),
	})

	wg.Wait()

	if outcome2.Result != ResultWaited {
		t.Errorf("expected ResultWaited for in-flight duplicate, got %d", outcome2.Result)
	}
	if outcome2.Response == nil || outcome2.Response.StatusCode != 200 {
		t.Error("expected in-flight waiter to get the stored response")
	}
}

func TestCancelInFlight(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled: true,
	})
	defer ci.Close()

	// First request proceeds
	r1 := newRequestWithKey("POST", "cancel-key")
	outcome1 := ci.Check(r1)
	if outcome1.Result != ResultProceed {
		t.Fatalf("expected ResultProceed, got %d", outcome1.Result)
	}

	// Second request waits
	var wg sync.WaitGroup
	var outcome2 CheckOutcome
	wg.Add(1)
	go func() {
		defer wg.Done()
		r2 := newRequestWithKey("POST", "cancel-key")
		outcome2 = ci.Check(r2)
	}()

	time.Sleep(50 * time.Millisecond)

	// Cancel without storing
	ci.CancelInFlight(outcome1.Key)

	wg.Wait()

	if outcome2.Result != ResultProceed {
		t.Errorf("expected ResultProceed after cancel, got %d", outcome2.Result)
	}
}

func TestPerClientScope(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled:  true,
		KeyScope: "per_client",
	})
	defer ci.Close()

	// Request from client A
	r1 := newRequestWithKey("POST", "shared-key")
	varCtx1 := variables.GetFromRequest(r1)
	varCtx1.Identity = &variables.Identity{ClientID: "client-a"}
	outcome1 := ci.Check(r1)
	if outcome1.Result != ResultProceed {
		t.Fatalf("expected ResultProceed, got %d", outcome1.Result)
	}
	ci.RecordResponse(outcome1.Key, &StoredResponse{
		StatusCode: 201,
		Headers:    http.Header{},
		Body:       []byte("a"),
	})

	// Same key from client B should proceed (different scope)
	r2 := newRequestWithKey("POST", "shared-key")
	varCtx2 := variables.GetFromRequest(r2)
	varCtx2.Identity = &variables.Identity{ClientID: "client-b"}
	outcome2 := ci.Check(r2)
	if outcome2.Result != ResultProceed {
		t.Errorf("expected ResultProceed for different client, got %d", outcome2.Result)
	}
	ci.CancelInFlight(outcome2.Key)

	// Same key from client A should get cached
	r3 := newRequestWithKey("POST", "shared-key")
	varCtx3 := variables.GetFromRequest(r3)
	varCtx3.Identity = &variables.Identity{ClientID: "client-a"}
	outcome3 := ci.Check(r3)
	if outcome3.Result != ResultCached {
		t.Errorf("expected ResultCached for same client, got %d", outcome3.Result)
	}
}

func TestCapturingWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := NewCapturingWriter(rec)

	cw.Header().Set("X-Custom", "value")
	cw.WriteHeader(201)
	cw.Write([]byte("response body"))

	resp := cw.ToStoredResponse()
	if resp.StatusCode != 201 {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}
	if string(resp.Body) != "response body" {
		t.Errorf("expected body 'response body', got %q", string(resp.Body))
	}
}

func TestReplayResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	resp := &StoredResponse{
		StatusCode: 201,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"id":"123"}`),
	}

	ReplayResponse(rec, resp)

	if rec.Code != 201 {
		t.Errorf("expected status 201, got %d", rec.Code)
	}
	if rec.Header().Get("X-Idempotent-Replayed") != "true" {
		t.Error("expected X-Idempotent-Replayed header")
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type header to be preserved")
	}
	if rec.Body.String() != `{"id":"123"}` {
		t.Errorf("expected body to be replayed, got %q", rec.Body.String())
	}
}

func TestMergeIdempotencyConfig(t *testing.T) {
	global := config.IdempotencyConfig{
		Enabled:      true,
		HeaderName:   "X-Global",
		TTL:          time.Hour,
		Methods:      []string{"POST"},
		KeyScope:     "global",
		Mode:         "local",
		MaxKeyLength: 256,
		MaxBodySize:  1024,
	}
	perRoute := config.IdempotencyConfig{
		Enabled:    true,
		HeaderName: "X-Custom",
		TTL:        2 * time.Hour,
	}
	merged := MergeIdempotencyConfig(perRoute, global)

	if merged.HeaderName != "X-Custom" {
		t.Errorf("expected X-Custom, got %s", merged.HeaderName)
	}
	if merged.TTL != 2*time.Hour {
		t.Errorf("expected 2h TTL, got %s", merged.TTL)
	}
	if len(merged.Methods) != 1 || merged.Methods[0] != "POST" {
		t.Error("expected Methods to inherit from global")
	}
	if merged.MaxKeyLength != 256 {
		t.Errorf("expected MaxKeyLength 256, got %d", merged.MaxKeyLength)
	}
}

func TestCustomMethods(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled: true,
		Methods: []string{"DELETE"},
	})
	defer ci.Close()

	// POST should be skipped since only DELETE is configured
	r := newRequestWithKey("POST", "key-1")
	outcome := ci.Check(r)
	if outcome.Result != ResultProceed {
		t.Errorf("expected ResultProceed for non-configured method, got %d", outcome.Result)
	}

	// DELETE should be checked
	r2 := newRequestWithKey("DELETE", "key-2")
	outcome2 := ci.Check(r2)
	if outcome2.Result != ResultProceed {
		t.Errorf("expected ResultProceed for new DELETE key, got %d", outcome2.Result)
	}
	if outcome2.Key == "" {
		t.Error("expected non-empty key for configured method")
	}
	ci.CancelInFlight(outcome2.Key)
}

func TestMaxBodySizeExceeded(t *testing.T) {
	ci := newTestIdempotency(config.IdempotencyConfig{
		Enabled:     true,
		MaxBodySize: 10,
	})
	defer ci.Close()

	r := newRequestWithKey("POST", "big-body-key")
	outcome := ci.Check(r)
	if outcome.Result != ResultProceed {
		t.Fatalf("expected ResultProceed, got %d", outcome.Result)
	}

	// Record a response that exceeds max body size
	ci.RecordResponse(outcome.Key, &StoredResponse{
		StatusCode: 200,
		Headers:    http.Header{},
		Body:       []byte("this body is way too long for storage"),
	})

	// The response should NOT have been stored
	r2 := newRequestWithKey("POST", "big-body-key")
	outcome2 := ci.Check(r2)
	if outcome2.Result == ResultCached {
		t.Error("expected response not to be cached when body exceeds max size")
	}
}

func TestMemoryStoreExpiry(t *testing.T) {
	store := NewMemoryStore(100 * time.Millisecond)
	defer store.Close()

	ctx := context.Background()
	resp := &StoredResponse{StatusCode: 200, Body: []byte("ok")}

	if err := store.Set(ctx, "expiry-key", resp, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}

	// Should be found immediately
	got, err := store.Get(ctx, "expiry-key")
	if err != nil || got == nil {
		t.Fatal("expected to find entry")
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	got, err = store.Get(ctx, "expiry-key")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Error("expected entry to be expired")
	}
}

func TestManagerAddAndGet(t *testing.T) {
	m := NewIdempotencyByRoute()

	err := m.AddRoute("route-1", config.IdempotencyConfig{Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if h := m.GetHandler("route-1"); h == nil {
		t.Error("expected handler for route-1")
	}
	if h := m.GetHandler("route-2"); h != nil {
		t.Error("expected nil for unknown route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route-1" {
		t.Errorf("expected [route-1], got %v", ids)
	}

	stats := m.Stats()
	if len(stats) != 1 {
		t.Errorf("expected 1 stat entry, got %d", len(stats))
	}

	m.CloseAll()
}

func TestManagerSkipsDisabled(t *testing.T) {
	m := NewIdempotencyByRoute()

	err := m.AddRoute("route-1", config.IdempotencyConfig{Enabled: false}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if h := m.GetHandler("route-1"); h != nil {
		t.Error("expected nil for disabled route")
	}
}

func TestCustomHeaderName(t *testing.T) {
	ci, _ := New("test", config.IdempotencyConfig{
		Enabled:    true,
		HeaderName: "X-Request-Id",
	}, nil)
	defer ci.Close()

	r := httptest.NewRequest("POST", "/", nil)
	varCtx := &variables.Context{Custom: make(map[string]string)}
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
	r = r.WithContext(ctx)
	r.Header.Set("X-Request-Id", "custom-123")

	outcome := ci.Check(r)
	if outcome.Result != ResultProceed {
		t.Fatalf("expected ResultProceed, got %d", outcome.Result)
	}
	if outcome.Key != "custom-123" {
		t.Errorf("expected key 'custom-123', got %q", outcome.Key)
	}
	ci.CancelInFlight(outcome.Key)
}
