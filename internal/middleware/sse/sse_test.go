package sse

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestNonSSEPassthrough(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true, HeartbeatInterval: time.Second})
	mw := h.Middleware()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api", nil)
	mw(inner).ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != `{"ok":true}` {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	// No SSE headers should be injected
	if rec.Header().Get("Cache-Control") == "no-store" {
		t.Fatal("non-SSE response should not have Cache-Control: no-store")
	}
	// Stats should show no connections
	stats := h.Stats()
	if stats["active_connections"].(int64) != 0 {
		t.Fatal("expected 0 active connections")
	}
	if stats["total_connections"].(int64) != 0 {
		t.Fatal("expected 0 total connections")
	}
}

func TestSSEPerEventFlushing(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true})
	mw := h.Middleware()

	flushCount := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Write two events in one call
		w.Write([]byte("data: event1\n\ndata: event2\n\n"))
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), onFlush: func() { flushCount++ }}
	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("SSE response should have Cache-Control: no-store")
	}

	body := rec.Body.String()
	if !strings.Contains(body, "data: event1\n\n") {
		t.Fatalf("missing event1 in body: %s", body)
	}
	if !strings.Contains(body, "data: event2\n\n") {
		t.Fatalf("missing event2 in body: %s", body)
	}

	// Should have flushed at least twice (once per event)
	if flushCount < 2 {
		t.Fatalf("expected at least 2 flushes, got %d", flushCount)
	}

	stats := h.Stats()
	if stats["total_events"].(int64) != 2 {
		t.Fatalf("expected 2 total events, got %d", stats["total_events"])
	}
	if stats["total_connections"].(int64) != 1 {
		t.Fatalf("expected 1 total connection, got %d", stats["total_connections"])
	}
	// Connection should be closed after handler returns
	if stats["active_connections"].(int64) != 0 {
		t.Fatalf("expected 0 active connections, got %d", stats["active_connections"])
	}
}

func TestSSERetryInjection(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true, RetryMS: 3000})
	mw := h.Middleware()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: hello\n\n"))
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.HasPrefix(body, "retry: 3000\n\n") {
		t.Fatalf("expected retry field at start of body, got: %s", body)
	}
}

func TestSSEConnectEvent(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true, ConnectEvent: "connected"})
	mw := h.Middleware()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: real\n\n"))
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "data: connected\n\n") {
		t.Fatalf("expected connect event in body, got: %s", body)
	}
	// Connect event should appear before real data
	connectIdx := strings.Index(body, "data: connected\n\n")
	realIdx := strings.Index(body, "data: real\n\n")
	if connectIdx >= realIdx {
		t.Fatal("connect event should appear before real events")
	}
}

func TestSSEDisconnectEvent(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true, DisconnectEvent: "bye"})
	mw := h.Middleware()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: hello\n\n"))
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.HasSuffix(body, "data: bye\n\n") {
		t.Fatalf("expected disconnect event at end of body, got: %s", body)
	}
}

func TestSSELastEventIDForwarding(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true})
	mw := h.Middleware()

	var receivedLastEventID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedLastEventID = r.Header.Get("Last-Event-ID")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/events", nil)
	req.Header.Set("Last-Event-ID", "42")
	mw(inner).ServeHTTP(rec, req)

	if receivedLastEventID != "42" {
		t.Fatalf("expected Last-Event-ID=42, got %q", receivedLastEventID)
	}
}

func TestSSEHeartbeat(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true, HeartbeatInterval: 50 * time.Millisecond})
	mw := h.Middleware()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Sleep to allow heartbeat to fire
		time.Sleep(200 * time.Millisecond)
	})

	pr, pw := io.Pipe()
	rec := &pipeResponseWriter{header: make(http.Header), pw: pw}

	done := make(chan struct{})
	var body string
	go func() {
		defer close(done)
		data, _ := io.ReadAll(pr)
		body = string(data)
	}()

	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)
	pw.Close()
	<-done

	if !strings.Contains(body, ": heartbeat\n\n") {
		t.Fatalf("expected heartbeat in body, got: %s", body)
	}

	if h.heartbeatsSent.Load() < 1 {
		t.Fatalf("expected at least 1 heartbeat sent, got %d", h.heartbeatsSent.Load())
	}
}

func TestSSEPartialEventBuffering(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true})
	mw := h.Middleware()

	flushCount := 0
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		// Write partial event (no boundary yet)
		w.Write([]byte("data: partial"))
		// At this point, no flush should have happened for event data
		// Now complete the event
		w.Write([]byte("\n\n"))
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder(), onFlush: func() { flushCount++ }}
	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "data: partial\n\n") {
		t.Fatalf("expected complete event in body, got: %s", body)
	}

	stats := h.Stats()
	if stats["total_events"].(int64) != 1 {
		t.Fatalf("expected 1 event, got %d", stats["total_events"])
	}
}

func TestSSECacheControlInjected(t *testing.T) {
	h := New(config.SSEConfig{Enabled: true})
	mw := h.Middleware()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "max-age=60")
		w.WriteHeader(200)
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control: no-store, got %q", rec.Header().Get("Cache-Control"))
	}
}

func TestSSERetryAndConnectCombined(t *testing.T) {
	h := New(config.SSEConfig{
		Enabled:      true,
		RetryMS:      5000,
		ConnectEvent: "hello",
	})
	mw := h.Middleware()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("data: msg\n\n"))
	})

	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest("GET", "/events", nil)
	mw(inner).ServeHTTP(rec, req)

	body := rec.Body.String()
	// retry should come first, then connect event, then real data
	retryIdx := strings.Index(body, "retry: 5000\n\n")
	connectIdx := strings.Index(body, "data: hello\n\n")
	msgIdx := strings.Index(body, "data: msg\n\n")

	if retryIdx < 0 || connectIdx < 0 || msgIdx < 0 {
		t.Fatalf("missing expected content in body: %s", body)
	}
	if retryIdx >= connectIdx || connectIdx >= msgIdx {
		t.Fatalf("wrong order: retry=%d, connect=%d, msg=%d", retryIdx, connectIdx, msgIdx)
	}
}

func TestSSEByRouteStats(t *testing.T) {
	mgr := NewSSEByRoute()
	mgr.AddRoute("route1", config.SSEConfig{Enabled: true})
	mgr.AddRoute("route2", config.SSEConfig{Enabled: true})

	if len(mgr.RouteIDs()) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(mgr.RouteIDs()))
	}

	h := mgr.GetHandler("route1")
	if h == nil {
		t.Fatal("expected handler for route1")
	}

	stats := mgr.Stats()
	if len(stats) != 2 {
		t.Fatalf("expected 2 stats entries, got %d", len(stats))
	}
}

// flushRecorder wraps httptest.ResponseRecorder with Flush support.
type flushRecorder struct {
	*httptest.ResponseRecorder
	onFlush func()
}

func (f *flushRecorder) Flush() {
	f.ResponseRecorder.Flush()
	if f.onFlush != nil {
		f.onFlush()
	}
}

func (f *flushRecorder) Unwrap() http.ResponseWriter {
	return f.ResponseRecorder
}

// pipeResponseWriter writes to a pipe for streaming tests.
type pipeResponseWriter struct {
	header      http.Header
	pw          *io.PipeWriter
	wroteHeader bool
}

func (p *pipeResponseWriter) Header() http.Header {
	return p.header
}

func (p *pipeResponseWriter) Write(b []byte) (int, error) {
	if !p.wroteHeader {
		p.wroteHeader = true
	}
	return p.pw.Write(b)
}

func (p *pipeResponseWriter) WriteHeader(code int) {
	p.wroteHeader = true
}

func (p *pipeResponseWriter) Flush() {}
