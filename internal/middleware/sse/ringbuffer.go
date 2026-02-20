package sse

import (
	"sync"
)

// RingBuffer is a thread-safe circular buffer of SSE events for client catch-up.
type RingBuffer struct {
	mu    sync.RWMutex
	items []SSEEvent
	head  int  // next write position
	full  bool // true when buffer has wrapped around
	size  int  // capacity
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	if size <= 0 {
		size = 256
	}
	return &RingBuffer{
		items: make([]SSEEvent, size),
		size:  size,
	}
}

// Push adds an event to the buffer, overwriting the oldest if full.
func (rb *RingBuffer) Push(evt SSEEvent) {
	rb.mu.Lock()
	rb.items[rb.head] = evt
	rb.head = (rb.head + 1) % rb.size
	if rb.head == 0 || rb.full {
		rb.full = true
	}
	rb.mu.Unlock()
}

// EventsSince returns all events after the given lastEventID.
// If lastEventID is empty, returns all buffered events.
// Returns events in chronological order.
func (rb *RingBuffer) EventsSince(lastEventID string) []SSEEvent {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	all := rb.allLocked()

	if lastEventID == "" {
		return all
	}

	// Find the position after lastEventID
	for i, evt := range all {
		if evt.ID == lastEventID {
			return all[i+1:]
		}
	}

	// lastEventID not found in buffer — return everything
	return all
}

// Len returns the number of events in the buffer.
func (rb *RingBuffer) Len() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	if rb.full {
		return rb.size
	}
	return rb.head
}

// LastEventID returns the ID of the most recent event, or "".
func (rb *RingBuffer) LastEventID() string {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	count := rb.head
	if rb.full {
		count = rb.size
	}
	if count == 0 {
		return ""
	}
	lastIdx := (rb.head - 1 + rb.size) % rb.size
	return rb.items[lastIdx].ID
}

// allLocked returns all buffered events in chronological order.
// Must be called with rb.mu held.
func (rb *RingBuffer) allLocked() []SSEEvent {
	if !rb.full {
		result := make([]SSEEvent, rb.head)
		copy(result, rb.items[:rb.head])
		return result
	}
	result := make([]SSEEvent, rb.size)
	copy(result, rb.items[rb.head:])
	copy(result[rb.size-rb.head:], rb.items[:rb.head])
	return result
}
