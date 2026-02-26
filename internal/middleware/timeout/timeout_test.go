package timeout

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestMiddlewareTimeoutFires(t *testing.T) {
	ct := New(config.TimeoutConfig{Request: 50 * time.Millisecond})
	mw := ct.Middleware()

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the timeout
		select {
		case <-time.After(200 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("expected 504, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" {
		t.Error("expected Retry-After header on 504")
	}
}

func TestMiddlewareNoTimeout(t *testing.T) {
	ct := New(config.TimeoutConfig{Request: 5 * time.Second})
	mw := ct.Middleware()

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra != "" {
		t.Error("did not expect Retry-After header on 200")
	}
}

func TestMiddlewarePassThroughWhenZero(t *testing.T) {
	ct := New(config.TimeoutConfig{})
	mw := ct.Middleware()

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// When no timeout is set, context should have no deadline
		if _, ok := r.Context().Deadline(); ok {
			t.Error("expected no deadline on context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("handler should have been called")
	}
}

func TestTimeoutByRouteAddGetStats(t *testing.T) {
	m := NewTimeoutByRoute()
	m.AddRoute("api", config.TimeoutConfig{
		Request: 30 * time.Second,
		Backend: 5 * time.Second,
		Idle:    60 * time.Second,
	})

	ct := m.GetTimeout("api")
	if ct == nil {
		t.Fatal("expected compiled timeout for route api")
	}
	if ct.Request != 30*time.Second {
		t.Errorf("expected Request 30s, got %v", ct.Request)
	}
	if ct.Backend != 5*time.Second {
		t.Errorf("expected Backend 5s, got %v", ct.Backend)
	}

	// Non-existent route
	if m.GetTimeout("nonexistent") != nil {
		t.Error("expected nil for non-existent route")
	}

	// RouteIDs
	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "api" {
		t.Errorf("expected [api], got %v", ids)
	}

	// Stats
	stats := m.Stats()
	if len(stats) != 1 {
		t.Fatalf("expected 1 stat entry, got %d", len(stats))
	}
	s, ok := stats["api"]
	if !ok {
		t.Fatal("expected stats for route api")
	}
	if s.Request != "30s" {
		t.Errorf("expected Request '30s', got %q", s.Request)
	}
	if s.Backend != "5s" {
		t.Errorf("expected Backend '5s', got %q", s.Backend)
	}
	if s.Idle != "1m0s" {
		t.Errorf("expected Idle '1m0s', got %q", s.Idle)
	}
}

func TestRetryAfterWriterInjectsOnlyOn504(t *testing.T) {
	// Test 504: should inject Retry-After
	rec := httptest.NewRecorder()
	w := &retryAfterWriter{ResponseWriter: rec, retryAfter: "30"}
	w.WriteHeader(http.StatusGatewayTimeout)
	if rec.Header().Get("Retry-After") != "30" {
		t.Error("expected Retry-After: 30 on 504")
	}

	// Test 200: should NOT inject
	rec2 := httptest.NewRecorder()
	w2 := &retryAfterWriter{ResponseWriter: rec2, retryAfter: "30"}
	w2.WriteHeader(http.StatusOK)
	if rec2.Header().Get("Retry-After") != "" {
		t.Error("did not expect Retry-After on 200")
	}
}

func TestMetrics(t *testing.T) {
	ct := New(config.TimeoutConfig{Request: 50 * time.Millisecond})
	mw := ct.Middleware()

	// Fast request
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	snap := ct.Metrics()
	if snap.TotalRequests != 1 {
		t.Errorf("expected TotalRequests=1, got %d", snap.TotalRequests)
	}
	if snap.RequestTimeouts != 0 {
		t.Errorf("expected RequestTimeouts=0, got %d", snap.RequestTimeouts)
	}

	// Slow request that times out
	handler2 := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	handler2.ServeHTTP(rec2, req2)

	snap2 := ct.Metrics()
	if snap2.TotalRequests != 2 {
		t.Errorf("expected TotalRequests=2, got %d", snap2.TotalRequests)
	}
	if snap2.RequestTimeouts != 1 {
		t.Errorf("expected RequestTimeouts=1, got %d", snap2.RequestTimeouts)
	}
}

func TestRetryAfterWriterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &retryAfterWriter{ResponseWriter: rec, retryAfter: "30"}
	w.Flush()
	// Just ensure it doesn't panic; httptest.ResponseRecorder implements Flusher
}

func TestRetryAfterWriterImplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &retryAfterWriter{ResponseWriter: rec, retryAfter: "30"}
	w.Write([]byte("data"))
	if rec.Code != http.StatusOK {
		t.Errorf("expected implicit 200, got %d", rec.Code)
	}
}
