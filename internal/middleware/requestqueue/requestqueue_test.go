package requestqueue

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestImmediatePass(t *testing.T) {
	q := New(10, 5*time.Second)
	handler := q.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	snap := q.Snapshot()
	if snap.Enqueued != 1 {
		t.Fatalf("expected enqueued=1, got %d", snap.Enqueued)
	}
	if snap.Dequeued != 1 {
		t.Fatalf("expected dequeued=1, got %d", snap.Dequeued)
	}
	if snap.TimedOut != 0 {
		t.Fatalf("expected timedOut=0, got %d", snap.TimedOut)
	}
}

func TestWaitAndProceed(t *testing.T) {
	q := New(1, 5*time.Second) // only 1 concurrent slot
	var wg sync.WaitGroup

	blocking := make(chan struct{})

	handler := q.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Block") == "true" {
			<-blocking
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Fill the slot with a blocking request.
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Block", "true")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()

	// Give the blocking request time to acquire the slot.
	time.Sleep(50 * time.Millisecond)

	// This request should wait and then proceed after we unblock.
	var secondDone atomic.Bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
		secondDone.Store(true)
	}()

	// Second request should be waiting.
	time.Sleep(50 * time.Millisecond)
	if secondDone.Load() {
		t.Fatal("second request should be waiting")
	}

	// Unblock the first request.
	close(blocking)
	wg.Wait()

	if !secondDone.Load() {
		t.Fatal("second request should have completed")
	}
}

func TestTimeout503(t *testing.T) {
	q := New(1, 100*time.Millisecond) // 1 slot, 100ms max wait
	blocking := make(chan struct{})
	defer close(blocking)

	handler := q.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Block") == "true" {
			<-blocking
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Fill the slot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Block", "true")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()
	time.Sleep(50 * time.Millisecond)

	// This request should time out.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	snap := q.Snapshot()
	if snap.TimedOut != 1 {
		t.Fatalf("expected timedOut=1, got %d", snap.TimedOut)
	}
}

func TestContextCancellation(t *testing.T) {
	q := New(1, 5*time.Second) // 1 slot, long wait
	blocking := make(chan struct{})
	defer close(blocking)

	handler := q.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Block") == "true" {
			<-blocking
		}
		w.WriteHeader(http.StatusOK)
	}))

	// Fill the slot.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Block", "true")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}()
	time.Sleep(50 * time.Millisecond)

	// Send a request with a context that will be cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel() // Cancel the client context.
	<-done

	snap := q.Snapshot()
	if snap.Rejected != 1 {
		t.Fatalf("expected rejected=1, got %d", snap.Rejected)
	}
}

func TestMetricsAccuracy(t *testing.T) {
	q := New(5, 5*time.Second)
	handler := q.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	n := 10
	for i := 0; i < n; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	snap := q.Snapshot()
	if snap.Enqueued != int64(n) {
		t.Fatalf("expected enqueued=%d, got %d", n, snap.Enqueued)
	}
	if snap.Dequeued != int64(n) {
		t.Fatalf("expected dequeued=%d, got %d", n, snap.Dequeued)
	}
	if snap.CurrentDepth != 0 {
		t.Fatalf("expected currentDepth=0, got %d", snap.CurrentDepth)
	}
	if snap.MaxDepth != 5 {
		t.Fatalf("expected maxDepth=5, got %d", snap.MaxDepth)
	}
}

func TestDefaults(t *testing.T) {
	q := New(0, 0)
	if q.maxDepth != 100 {
		t.Fatalf("expected default maxDepth=100, got %d", q.maxDepth)
	}
	if q.maxWait != 30*time.Second {
		t.Fatalf("expected default maxWait=30s, got %v", q.maxWait)
	}
}

func TestByRouteManager(t *testing.T) {
	mgr := NewRequestQueueByRoute()
	mgr.AddRoute("route1", config.RequestQueueConfig{
		Enabled:  true,
		MaxDepth: 50,
		MaxWait:  10 * time.Second,
	})

	q := mgr.GetQueue("route1")
	if q == nil {
		t.Fatal("expected queue for route1")
	}
	if q.maxDepth != 50 {
		t.Fatalf("expected maxDepth=50, got %d", q.maxDepth)
	}

	if mgr.GetQueue("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	stats := mgr.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Fatal("expected stats for route1")
	}
}

func TestMergeRequestQueueConfig(t *testing.T) {
	global := config.RequestQueueConfig{
		Enabled:  true,
		MaxDepth: 200,
		MaxWait:  60 * time.Second,
	}
	route := config.RequestQueueConfig{
		Enabled:  true,
		MaxDepth: 50,
	}

	merged := MergeRequestQueueConfig(route, global)
	if merged.MaxDepth != 50 {
		t.Fatalf("expected maxDepth=50, got %d", merged.MaxDepth)
	}
	if merged.MaxWait != 60*time.Second {
		t.Fatalf("expected maxWait=60s from global, got %v", merged.MaxWait)
	}
}
