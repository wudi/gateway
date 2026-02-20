package sse

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

func TestHubFanout(t *testing.T) {
	// Create upstream SSE server that sends events
	eventCh := make(chan string, 10)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		flusher.Flush()

		for evt := range eventCh {
			fmt.Fprint(w, evt)
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	backends := []*loadbalancer.Backend{{URL: upstream.URL, Weight: 1, Healthy: true}}
	bal := loadbalancer.NewRoundRobin(backends)

	hub := NewHub(config.SSEFanoutConfig{
		Enabled:          true,
		BufferSize:       10,
		ClientBufferSize: 10,
		ReconnectDelay:   100 * time.Millisecond,
	}, bal)
	hub.Start()
	defer hub.Stop()

	// Wait for connection to establish
	time.Sleep(200 * time.Millisecond)

	if !hub.connected.Load() {
		t.Fatal("hub should be connected")
	}

	// Send an event
	eventCh <- "id: 1\ndata: hello\n\n"
	time.Sleep(100 * time.Millisecond)

	// Check ring buffer received the event
	if hub.buffer.Len() != 1 {
		t.Errorf("expected 1 event in buffer, got %d", hub.buffer.Len())
	}

	stats := hub.Stats()
	if !stats["hub_connected"].(bool) {
		t.Error("expected hub_connected=true")
	}

	close(eventCh)
}

func TestHubServeClientCatchup(t *testing.T) {
	hub := NewHub(config.SSEFanoutConfig{
		Enabled:          true,
		BufferSize:       10,
		ClientBufferSize: 10,
	}, nil)

	hub.buffer.Push(SSEEvent{
		ID:   "1",
		Data: "first",
		Raw:  []byte("id: 1\ndata: first\n\n"),
	})
	hub.buffer.Push(SSEEvent{
		ID:   "2",
		Data: "second",
		Raw:  []byte("id: 2\ndata: second\n\n"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	hub.ServeClient(rec, r)

	body := rec.Body.String()
	if !strings.Contains(body, "data: first") {
		t.Errorf("expected catch-up event 'first' in body, got: %s", body)
	}
	if !strings.Contains(body, "data: second") {
		t.Errorf("expected catch-up event 'second' in body, got: %s", body)
	}
}

func TestHubServeClientCatchupSinceLastID(t *testing.T) {
	hub := NewHub(config.SSEFanoutConfig{
		Enabled:          true,
		BufferSize:       10,
		ClientBufferSize: 10,
	}, nil)

	hub.buffer.Push(SSEEvent{ID: "1", Data: "first", Raw: []byte("id: 1\ndata: first\n\n")})
	hub.buffer.Push(SSEEvent{ID: "2", Data: "second", Raw: []byte("id: 2\ndata: second\n\n")})
	hub.buffer.Push(SSEEvent{ID: "3", Data: "third", Raw: []byte("id: 3\ndata: third\n\n")})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	r := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	r.Header.Set("Last-Event-ID", "1")
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	hub.ServeClient(rec, r)

	body := rec.Body.String()
	if strings.Contains(body, "data: first") {
		t.Error("should not include event before Last-Event-ID")
	}
	if !strings.Contains(body, "data: second") {
		t.Error("should include event after Last-Event-ID")
	}
	if !strings.Contains(body, "data: third") {
		t.Error("should include event after Last-Event-ID")
	}
}

func TestHubEventFiltering(t *testing.T) {
	hub := NewHub(config.SSEFanoutConfig{
		Enabled:          true,
		BufferSize:       10,
		ClientBufferSize: 10,
		EventFiltering:   true,
		FilterParam:      "type",
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/events?type=chat", nil).WithContext(ctx)
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.ServeClient(rec, r)
	}()

	time.Sleep(50 * time.Millisecond)

	// Broadcast a chat event (should pass filter)
	hub.broadcast(SSEEvent{Event: "chat", Data: "hello", Raw: []byte("event: chat\ndata: hello\n\n")})
	// Broadcast a system event (should be filtered)
	hub.broadcast(SSEEvent{Event: "system", Data: "ignored", Raw: []byte("event: system\ndata: ignored\n\n")})

	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	if !strings.Contains(body, "data: hello") {
		t.Errorf("expected chat event in body, got: %s", body)
	}
	if strings.Contains(body, "data: ignored") {
		t.Errorf("system event should have been filtered out, body: %s", body)
	}
}

func TestHubStats(t *testing.T) {
	hub := NewHub(config.SSEFanoutConfig{
		Enabled:    true,
		BufferSize: 10,
	}, nil)

	hub.buffer.Push(SSEEvent{ID: "1", Raw: []byte("id: 1\ndata: x\n\n")})
	hub.lastEventID.Store("1")

	stats := hub.Stats()
	if stats["buffer_used"].(int) != 1 {
		t.Errorf("expected buffer_used=1, got %v", stats["buffer_used"])
	}
	if stats["last_event_id"].(string) != "1" {
		t.Errorf("expected last_event_id='1', got %v", stats["last_event_id"])
	}
}

func TestHubClientCount(t *testing.T) {
	hub := NewHub(config.SSEFanoutConfig{
		Enabled:          true,
		BufferSize:       10,
		ClientBufferSize: 10,
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "/events", nil).WithContext(ctx)
	rec := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.ServeClient(rec, r)
	}()

	time.Sleep(50 * time.Millisecond)

	if hub.clientCount.Load() != 1 {
		t.Errorf("expected 1 client, got %d", hub.clientCount.Load())
	}

	cancel()
	<-done

	if hub.clientCount.Load() != 0 {
		t.Errorf("expected 0 clients after disconnect, got %d", hub.clientCount.Load())
	}
}
