package sse

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// Client represents a connected SSE fan-out client.
type Client struct {
	id      uint64
	events  chan SSEEvent
	filters map[string]bool // event type filter; nil = no filtering
	dropped atomic.Int64
	closed  atomic.Bool
}

var clientIDCounter atomic.Uint64

// newClient creates a new fan-out client with the given buffer size and optional event filter.
func newClient(bufferSize int, filterTypes []string) *Client {
	c := &Client{
		id:     clientIDCounter.Add(1),
		events: make(chan SSEEvent, bufferSize),
	}
	if len(filterTypes) > 0 {
		c.filters = make(map[string]bool, len(filterTypes))
		for _, t := range filterTypes {
			c.filters[t] = true
		}
	}
	return c
}

// Send tries to send an event to the client. Returns false if the client's
// buffer is full (event is dropped) or the client is closed.
func (c *Client) Send(evt SSEEvent) bool {
	if c.closed.Load() {
		return false
	}

	// Apply event type filter
	if c.filters != nil && evt.Event != "" && !c.filters[evt.Event] {
		return true // filtered out, not a failure
	}

	select {
	case c.events <- evt:
		return true
	default:
		c.dropped.Add(1)
		return false
	}
}

// Close closes the client channel.
func (c *Client) Close() {
	if c.closed.CompareAndSwap(false, true) {
		close(c.events)
	}
}

// serveHTTP writes SSE events to the HTTP response for this client.
func (c *Client) serveHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Del("Content-Length")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-c.events:
			if !ok {
				return // channel closed
			}
			if _, err := w.Write(evt.Raw); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// writeEvent writes a single SSE event directly (for catch-up events).
func writeEvent(w http.ResponseWriter, evt SSEEvent) error {
	_, err := w.Write(evt.Raw)
	return err
}

// writeComment writes an SSE comment.
func writeComment(w http.ResponseWriter, flusher http.Flusher, comment string) {
	fmt.Fprintf(w, ": %s\n\n", comment)
	flusher.Flush()
}
