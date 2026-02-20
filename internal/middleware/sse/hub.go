package sse

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

// Hub manages a single upstream SSE connection and broadcasts events to connected clients.
type Hub struct {
	cfg      config.SSEFanoutConfig
	balancer loadbalancer.Balancer

	clients sync.Map // uint64 → *Client
	buffer  *RingBuffer

	// state
	connected    atomic.Bool
	reconnects   atomic.Int64
	droppedTotal atomic.Int64
	lastEventID  atomic.Value // string
	clientCount  atomic.Int64

	cancel context.CancelFunc
	done   chan struct{}
}

// NewHub creates a fan-out hub for the given balancer.
func NewHub(cfg config.SSEFanoutConfig, balancer loadbalancer.Balancer) *Hub {
	bufSize := cfg.BufferSize
	if bufSize <= 0 {
		bufSize = 256
	}
	return &Hub{
		cfg:      cfg,
		balancer: balancer,
		buffer:   NewRingBuffer(bufSize),
		done:     make(chan struct{}),
	}
}

// Start begins the upstream connection loop in a background goroutine.
func (h *Hub) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go h.connectLoop(ctx)
}

// Stop shuts down the hub: disconnects upstream, closes all clients.
func (h *Hub) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	<-h.done

	// Close all clients
	h.clients.Range(func(key, value interface{}) bool {
		if c, ok := value.(*Client); ok {
			c.Close()
		}
		h.clients.Delete(key)
		return true
	})
}

// ServeClient handles an incoming client connection.
func (h *Hub) ServeClient(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	clientBuf := h.cfg.ClientBufferSize
	if clientBuf <= 0 {
		clientBuf = 64
	}

	// Parse event type filter from query param
	var filters []string
	if h.cfg.EventFiltering {
		param := h.cfg.FilterParam
		if param == "" {
			param = "event_type"
		}
		if filterStr := r.URL.Query().Get(param); filterStr != "" {
			filters = strings.Split(filterStr, ",")
		}
	}

	client := newClient(clientBuf, filters)
	h.clients.Store(client.id, client)
	h.clientCount.Add(1)

	defer func() {
		client.Close()
		h.clients.Delete(client.id)
		h.clientCount.Add(-1)
	}()

	// Write SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Send catch-up events from ring buffer
	lastID := r.Header.Get("Last-Event-ID")
	catchup := h.buffer.EventsSince(lastID)
	for _, evt := range catchup {
		// Apply filter for catch-up events too
		if client.filters != nil && evt.Event != "" && !client.filters[evt.Event] {
			continue
		}
		if err := writeEvent(w, evt); err != nil {
			return
		}
	}
	flusher.Flush()

	// Stream events from channel
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-client.events:
			if !ok {
				return
			}
			if _, err := w.Write(evt.Raw); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// connectLoop maintains the upstream SSE connection with reconnection.
func (h *Hub) connectLoop(ctx context.Context) {
	defer close(h.done)

	reconnectDelay := h.cfg.ReconnectDelay
	if reconnectDelay <= 0 {
		reconnectDelay = time.Second
	}

	attempts := 0
	for {
		select {
		case <-ctx.Done():
			h.connected.Store(false)
			return
		default:
		}

		if h.cfg.MaxReconnects > 0 && attempts >= h.cfg.MaxReconnects {
			h.connected.Store(false)
			return
		}

		err := h.connectUpstream(ctx)
		h.connected.Store(false)

		if ctx.Err() != nil {
			return
		}

		_ = err // logged upstream
		attempts++
		h.reconnects.Add(1)

		// Wait before reconnecting
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconnectDelay):
		}
	}
}

// connectUpstream establishes a single upstream SSE connection and reads events.
func (h *Hub) connectUpstream(ctx context.Context) error {
	backend := h.balancer.Next()
	if backend == nil {
		return fmt.Errorf("no backends available")
	}

	url := strings.TrimRight(backend.URL, "/")
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-store")

	// Forward last event ID for resumption
	if lastID, ok := h.lastEventID.Load().(string); ok && lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upstream connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream returned status %d", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		return fmt.Errorf("upstream content-type %q is not text/event-stream", ct)
	}

	h.connected.Store(true)
	return h.readEvents(ctx, resp.Body)
}

// readEvents reads SSE events from the upstream body and broadcasts them.
func (h *Hub) readEvents(ctx context.Context, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1MB max line

	var eventBuf bytes.Buffer

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Text()
		eventBuf.WriteString(line)
		eventBuf.WriteByte('\n')

		// Empty line = event boundary
		if line == "" {
			raw := make([]byte, eventBuf.Len())
			copy(raw, eventBuf.Bytes())
			eventBuf.Reset()

			// Skip comment-only or truly empty events
			if len(bytes.TrimSpace(raw)) == 0 {
				continue
			}

			evt := parseSSEEvent(raw)

			// Update last event ID
			if evt.ID != "" {
				h.lastEventID.Store(evt.ID)
			}

			// Store in ring buffer
			h.buffer.Push(evt)

			// Broadcast to all clients
			h.broadcast(evt)
		}
	}

	return scanner.Err()
}

// broadcast sends an event to all connected clients.
func (h *Hub) broadcast(evt SSEEvent) {
	h.clients.Range(func(key, value interface{}) bool {
		client := value.(*Client)
		if !client.Send(evt) {
			h.droppedTotal.Add(1)
		}
		return true
	})
}

// Stats returns hub statistics.
func (h *Hub) Stats() map[string]interface{} {
	lastID := ""
	if v, ok := h.lastEventID.Load().(string); ok {
		lastID = v
	}
	return map[string]interface{}{
		"hub_connected":  h.connected.Load(),
		"clients":        h.clientCount.Load(),
		"buffer_used":    h.buffer.Len(),
		"reconnects":     h.reconnects.Load(),
		"dropped_events": h.droppedTotal.Load(),
		"last_event_id":  lastID,
	}
}
