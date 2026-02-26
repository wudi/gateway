package streaming

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

// spyFlusher is an http.ResponseWriter that records Flush() calls.
type spyFlusher struct {
	http.ResponseWriter
	flushCount atomic.Int64
}

func (sf *spyFlusher) Flush() {
	sf.flushCount.Add(1)
	if f, ok := sf.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func TestDisableBuffering_FlushesOnEveryWrite(t *testing.T) {
	h := New(config.StreamingConfig{
		Enabled:          true,
		DisableBuffering: true,
	})

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("chunk1"))
		w.Write([]byte("chunk2"))
		w.Write([]byte("chunk3"))
	})

	handler := h.Middleware()(backend)

	rec := httptest.NewRecorder()
	spy := &spyFlusher{ResponseWriter: rec}
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(spy, req)

	if spy.flushCount.Load() != 3 {
		t.Errorf("expected 3 flushes, got %d", spy.flushCount.Load())
	}

	if rec.Body.String() != "chunk1chunk2chunk3" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}

	if h.FlushedWrites() != 3 {
		t.Errorf("expected 3 flushed writes tracked, got %d", h.FlushedWrites())
	}
}

func TestFlushInterval_PeriodicFlush(t *testing.T) {
	h := New(config.StreamingConfig{
		Enabled:       true,
		FlushInterval: 20 * time.Millisecond,
	})

	// Use a channel to synchronise the backend: it writes one chunk then
	// blocks long enough for the ticker to fire at least once.
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
		// Wait long enough for at least one tick to fire.
		time.Sleep(80 * time.Millisecond)
	})

	handler := h.Middleware()(backend)

	rec := httptest.NewRecorder()
	spy := &spyFlusher{ResponseWriter: rec}
	req := httptest.NewRequest("GET", "/stream", nil)
	handler.ServeHTTP(spy, req)

	if spy.flushCount.Load() < 1 {
		t.Errorf("expected at least 1 periodic flush, got %d", spy.flushCount.Load())
	}

	if rec.Body.String() != "data" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestFlushInterval_StopsOnContextCancel(t *testing.T) {
	h := New(config.StreamingConfig{
		Enabled:       true,
		FlushInterval: 10 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
		cancel()
		// Wait a bit to give the goroutine time to see the cancellation.
		time.Sleep(50 * time.Millisecond)
	})

	handler := h.Middleware()(backend)

	rec := httptest.NewRecorder()
	spy := &spyFlusher{ResponseWriter: rec}
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	handler.ServeHTTP(spy, req)

	// Mainly checking that the goroutine terminates cleanly (no panic, no leak).
	if rec.Body.String() != "ok" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestNoStreamingConfig_PassThrough(t *testing.T) {
	h := New(config.StreamingConfig{
		Enabled: true,
		// Neither disable_buffering nor flush_interval set.
	})

	called := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte("pass"))
	})

	handler := h.Middleware()(backend)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected backend to be called")
	}
	if rec.Body.String() != "pass" {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}

	// No flushed writes should be tracked.
	if h.FlushedWrites() != 0 {
		t.Errorf("expected 0 flushed writes, got %d", h.FlushedWrites())
	}
}

func TestStats_Tracking(t *testing.T) {
	h := New(config.StreamingConfig{
		Enabled:          true,
		DisableBuffering: true,
	})

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("x"))
	})

	handler := h.Middleware()(backend)

	rec := httptest.NewRecorder()
	spy := &spyFlusher{ResponseWriter: rec}

	handler.ServeHTTP(spy, httptest.NewRequest("GET", "/", nil))
	handler.ServeHTTP(spy, httptest.NewRequest("GET", "/", nil))

	if h.TotalRequests() != 2 {
		t.Errorf("expected 2 total requests, got %d", h.TotalRequests())
	}
	if h.FlushedWrites() != 2 {
		t.Errorf("expected 2 flushed writes, got %d", h.FlushedWrites())
	}
}

func TestStreamByRoute(t *testing.T) {
	m := NewStreamByRoute()
	m.AddRoute("route1", config.StreamingConfig{
		Enabled:          true,
		DisableBuffering: true,
	})

	if m.GetHandler("route1") == nil {
		t.Error("expected handler for route1")
	}
	if m.GetHandler("nonexistent") != nil {
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

func TestStreamByRoute_Stats(t *testing.T) {
	m := NewStreamByRoute()
	m.AddRoute("r1", config.StreamingConfig{
		Enabled:          true,
		DisableBuffering: true,
	})

	h := m.GetHandler("r1")
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	})
	handler := h.Middleware()(backend)

	rec := httptest.NewRecorder()
	spy := &spyFlusher{ResponseWriter: rec}
	handler.ServeHTTP(spy, httptest.NewRequest("GET", "/", nil))

	stats := m.Stats()
	r1Stats, ok := stats["r1"]
	if !ok {
		t.Fatal("expected stats for r1")
	}
	m1 := r1Stats.(map[string]interface{})
	if m1["total_requests"].(int64) != 1 {
		t.Errorf("expected 1 total request, got %v", m1["total_requests"])
	}
	if m1["flushed_writes"].(int64) != 1 {
		t.Errorf("expected 1 flushed write, got %v", m1["flushed_writes"])
	}
}
