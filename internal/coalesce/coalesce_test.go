package coalesce

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestShouldCoalesce(t *testing.T) {
	c := New(config.CoalesceConfig{Enabled: true})

	tests := []struct {
		method string
		want   bool
	}{
		{"GET", true},
		{"HEAD", true},
		{"POST", false},
		{"PUT", false},
		{"DELETE", false},
		{"PATCH", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, "/test", nil)
		if got := c.ShouldCoalesce(r); got != tt.want {
			t.Errorf("ShouldCoalesce(%s) = %v, want %v", tt.method, got, tt.want)
		}
	}
}

func TestShouldCoalesceCustomMethods(t *testing.T) {
	c := New(config.CoalesceConfig{
		Enabled: true,
		Methods: []string{"GET", "POST"},
	})

	r := httptest.NewRequest("POST", "/graphql", nil)
	if !c.ShouldCoalesce(r) {
		t.Error("POST should be coalesced when configured")
	}

	r = httptest.NewRequest("HEAD", "/test", nil)
	if c.ShouldCoalesce(r) {
		t.Error("HEAD should not be coalesced when not in methods list")
	}
}

func TestBuildKey(t *testing.T) {
	c := New(config.CoalesceConfig{
		Enabled:    true,
		KeyHeaders: []string{"Accept", "Authorization"},
	})

	r1 := httptest.NewRequest("GET", "/api/products?page=1", nil)
	r1.Header.Set("Accept", "application/json")
	r1.Header.Set("Authorization", "Bearer token1")

	r2 := httptest.NewRequest("GET", "/api/products?page=1", nil)
	r2.Header.Set("Accept", "application/json")
	r2.Header.Set("Authorization", "Bearer token1")

	r3 := httptest.NewRequest("GET", "/api/products?page=1", nil)
	r3.Header.Set("Accept", "application/json")
	r3.Header.Set("Authorization", "Bearer token2")

	key1 := c.BuildKey(r1)
	key2 := c.BuildKey(r2)
	key3 := c.BuildKey(r3)

	if key1 != key2 {
		t.Error("identical requests should produce the same key")
	}
	if key1 == key3 {
		t.Error("requests with different headers should produce different keys")
	}

	// Different path → different key
	r4 := httptest.NewRequest("GET", "/api/other?page=1", nil)
	r4.Header.Set("Accept", "application/json")
	r4.Header.Set("Authorization", "Bearer token1")
	key4 := c.BuildKey(r4)
	if key1 == key4 {
		t.Error("requests with different paths should produce different keys")
	}
}

func TestExecuteCoalescence(t *testing.T) {
	c := New(config.CoalesceConfig{
		Enabled: true,
		Timeout: 5 * time.Second,
	})

	var callCount atomic.Int64
	fn := func() (*Response, error) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond) // simulate backend latency
		return &Response{
			StatusCode: 200,
			Headers:    http.Header{"X-Test": {"value"}},
			Body:       []byte("response body"),
		}, nil
	}

	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)

	results := make([]*Response, n)
	shared := make([]bool, n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/test", nil)
			results[idx], shared[idx], errs[idx] = c.Execute(r.Context(), "same-key", fn)
		}(i)
	}

	wg.Wait()

	// All should succeed
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error: %v", i, err)
		}
	}

	// Only 1 fn call
	if got := callCount.Load(); got != 1 {
		t.Errorf("fn called %d times, want 1", got)
	}

	// All get same response
	for i, resp := range results {
		if resp.StatusCode != 200 {
			t.Errorf("goroutine %d: status = %d, want 200", i, resp.StatusCode)
		}
		if string(resp.Body) != "response body" {
			t.Errorf("goroutine %d: unexpected body", i)
		}
	}

	// All N callers see Shared=true when the result was shared
	sharedCount := 0
	for _, s := range shared {
		if s {
			sharedCount++
		}
	}
	if sharedCount != n {
		t.Errorf("shared count = %d, want %d", sharedCount, n)
	}

	stats := c.Stats()
	if stats.GroupsCreated != 1 {
		t.Errorf("GroupsCreated = %d, want 1", stats.GroupsCreated)
	}
	if stats.RequestsCoalesced != int64(n) {
		t.Errorf("RequestsCoalesced = %d, want %d", stats.RequestsCoalesced, n)
	}
}

func TestExecuteDifferentKeys(t *testing.T) {
	c := New(config.CoalesceConfig{
		Enabled: true,
		Timeout: 5 * time.Second,
	})

	var callCount atomic.Int64
	fn := func() (*Response, error) {
		callCount.Add(1)
		return &Response{StatusCode: 200, Body: []byte("ok")}, nil
	}

	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		go func(idx int) {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/test", nil)
			c.Execute(r.Context(), "key-"+string(rune('a'+idx)), fn)
		}(i)
	}
	wg.Wait()

	if got := callCount.Load(); got != 3 {
		t.Errorf("fn called %d times, want 3 (different keys)", got)
	}
}

func TestExecuteTimeout(t *testing.T) {
	c := New(config.CoalesceConfig{
		Enabled: true,
		Timeout: 50 * time.Millisecond, // very short timeout
	})

	var callCount atomic.Int64
	fn := func() (*Response, error) {
		callCount.Add(1)
		time.Sleep(200 * time.Millisecond) // longer than timeout
		return &Response{StatusCode: 200, Body: []byte("ok")}, nil
	}

	// First caller starts the singleflight group
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		r := httptest.NewRequest("GET", "/test", nil)
		c.Execute(r.Context(), "slow-key", fn)
	}()

	// Small delay to ensure first caller starts
	time.Sleep(10 * time.Millisecond)

	// Second caller should timeout and call fn independently
	go func() {
		defer wg.Done()
		r := httptest.NewRequest("GET", "/test", nil)
		resp, _, err := c.Execute(r.Context(), "slow-key", fn)
		if err != nil {
			t.Errorf("timed-out caller got error: %v", err)
			return
		}
		if resp.StatusCode != 200 {
			t.Errorf("timed-out caller got status %d, want 200", resp.StatusCode)
		}
	}()

	wg.Wait()

	// Both primary and timed-out caller should have called fn
	if got := callCount.Load(); got < 2 {
		t.Errorf("fn called %d times, want >= 2 (timeout fallback)", got)
	}

	stats := c.Stats()
	if stats.Timeouts < 1 {
		t.Errorf("Timeouts = %d, want >= 1", stats.Timeouts)
	}
}

func TestBufferingWriter(t *testing.T) {
	bw := newBufferingWriter()

	bw.Header().Set("Content-Type", "application/json")
	bw.Header().Set("X-Custom", "value")
	bw.WriteHeader(201)
	bw.Write([]byte(`{"id": 1}`))

	resp := bw.Response()
	if resp.StatusCode != 201 {
		t.Errorf("StatusCode = %d, want 201", resp.StatusCode)
	}
	if resp.Headers.Get("Content-Type") != "application/json" {
		t.Error("Content-Type header not captured")
	}
	if resp.Headers.Get("X-Custom") != "value" {
		t.Error("X-Custom header not captured")
	}
	if string(resp.Body) != `{"id": 1}` {
		t.Errorf("Body = %q, want %q", string(resp.Body), `{"id": 1}`)
	}
}

func TestWriteResponse(t *testing.T) {
	resp := &Response{
		StatusCode: 201,
		Headers:    http.Header{"X-Test": {"val"}, "Content-Type": {"text/plain"}},
		Body:       []byte("hello"),
	}

	// Test without shared
	rec := httptest.NewRecorder()
	WriteResponse(rec, resp, false)
	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if rec.Header().Get("X-Coalesced") != "" {
		t.Error("X-Coalesced should not be set when shared=false")
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello")
	}

	// Test with shared
	rec2 := httptest.NewRecorder()
	WriteResponse(rec2, resp, true)
	if rec2.Header().Get("X-Coalesced") != "true" {
		t.Error("X-Coalesced should be 'true' when shared=true")
	}
}

func TestCoalesceByRoute(t *testing.T) {
	mgr := NewCoalesceByRoute()

	mgr.AddRoute("route-1", config.CoalesceConfig{Enabled: true, Timeout: 5 * time.Second})
	mgr.AddRoute("route-2", config.CoalesceConfig{Enabled: true, Methods: []string{"POST"}})

	if c := mgr.GetCoalescer("route-1"); c == nil {
		t.Error("GetCoalescer(route-1) should not be nil")
	}
	if c := mgr.GetCoalescer("route-2"); c == nil {
		t.Error("GetCoalescer(route-2) should not be nil")
	}
	if c := mgr.GetCoalescer("route-3"); c != nil {
		t.Error("GetCoalescer(route-3) should be nil")
	}

	ids := mgr.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("RouteIDs count = %d, want 2", len(ids))
	}

	stats := mgr.Stats()
	if len(stats) != 2 {
		t.Errorf("Stats count = %d, want 2", len(stats))
	}
}

func TestServeCoalesced(t *testing.T) {
	c := New(config.CoalesceConfig{Enabled: true, Timeout: 5 * time.Second})

	var callCount atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("X-Backend", "true")
		w.WriteHeader(200)
		w.Write([]byte("backend response"))
	})

	const n = 5
	var wg sync.WaitGroup
	wg.Add(n)

	recorders := make([]*httptest.ResponseRecorder, n)
	for i := 0; i < n; i++ {
		recorders[i] = httptest.NewRecorder()
		go func(idx int) {
			defer wg.Done()
			r := httptest.NewRequest("GET", "/api/test", nil)
			c.ServeCoalesced(recorders[idx], r, handler)
		}(i)
	}

	wg.Wait()

	if got := callCount.Load(); got != 1 {
		t.Errorf("handler called %d times, want 1", got)
	}

	for i, rec := range recorders {
		if rec.Code != 200 {
			t.Errorf("recorder %d: status = %d, want 200", i, rec.Code)
		}
		if rec.Body.String() != "backend response" {
			t.Errorf("recorder %d: body = %q", i, rec.Body.String())
		}
		if rec.Header().Get("X-Backend") != "true" {
			t.Errorf("recorder %d: missing X-Backend header", i)
		}
	}

	// All N callers see X-Coalesced: true because singleflight reports
	// Shared=true for all callers when the result was shared
	coalescedCount := 0
	for _, rec := range recorders {
		if rec.Header().Get("X-Coalesced") == "true" {
			coalescedCount++
		}
	}
	if coalescedCount != n {
		t.Errorf("coalesced count = %d, want %d", coalescedCount, n)
	}
}
