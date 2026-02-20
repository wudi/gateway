package sse

import (
	"fmt"
	"testing"
)

func TestRingBufferPushAndLen(t *testing.T) {
	rb := NewRingBuffer(5)

	if rb.Len() != 0 {
		t.Errorf("expected empty buffer, got len %d", rb.Len())
	}

	for i := 0; i < 3; i++ {
		rb.Push(SSEEvent{ID: fmt.Sprintf("%d", i), Data: fmt.Sprintf("event-%d", i), Raw: []byte(fmt.Sprintf("id: %d\ndata: event-%d\n\n", i, i))})
	}

	if rb.Len() != 3 {
		t.Errorf("expected 3, got %d", rb.Len())
	}
}

func TestRingBufferWrapAround(t *testing.T) {
	rb := NewRingBuffer(3)

	for i := 0; i < 5; i++ {
		rb.Push(SSEEvent{ID: fmt.Sprintf("%d", i), Data: fmt.Sprintf("e%d", i), Raw: []byte(fmt.Sprintf("id: %d\n\n", i))})
	}

	if rb.Len() != 3 {
		t.Errorf("expected 3 (capacity), got %d", rb.Len())
	}

	all := rb.EventsSince("")
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}

	// Should have events 2, 3, 4 (oldest overwritten)
	if all[0].ID != "2" {
		t.Errorf("expected first event ID '2', got %q", all[0].ID)
	}
	if all[2].ID != "4" {
		t.Errorf("expected last event ID '4', got %q", all[2].ID)
	}
}

func TestRingBufferEventsSince(t *testing.T) {
	rb := NewRingBuffer(10)

	for i := 1; i <= 5; i++ {
		rb.Push(SSEEvent{ID: fmt.Sprintf("%d", i), Data: fmt.Sprintf("e%d", i), Raw: []byte(fmt.Sprintf("id: %d\n\n", i))})
	}

	// Get events since ID "3"
	events := rb.EventsSince("3")
	if len(events) != 2 {
		t.Fatalf("expected 2 events after ID 3, got %d", len(events))
	}
	if events[0].ID != "4" {
		t.Errorf("expected first event ID '4', got %q", events[0].ID)
	}
	if events[1].ID != "5" {
		t.Errorf("expected second event ID '5', got %q", events[1].ID)
	}
}

func TestRingBufferEventsSinceUnknownID(t *testing.T) {
	rb := NewRingBuffer(10)

	for i := 1; i <= 3; i++ {
		rb.Push(SSEEvent{ID: fmt.Sprintf("%d", i), Raw: []byte(fmt.Sprintf("id: %d\n\n", i))})
	}

	// Unknown ID: return everything
	events := rb.EventsSince("999")
	if len(events) != 3 {
		t.Errorf("expected 3 events for unknown ID, got %d", len(events))
	}
}

func TestRingBufferLastEventID(t *testing.T) {
	rb := NewRingBuffer(5)

	if rb.LastEventID() != "" {
		t.Error("expected empty last event ID for empty buffer")
	}

	rb.Push(SSEEvent{ID: "a", Raw: []byte("id: a\n\n")})
	rb.Push(SSEEvent{ID: "b", Raw: []byte("id: b\n\n")})

	if rb.LastEventID() != "b" {
		t.Errorf("expected last event ID 'b', got %q", rb.LastEventID())
	}
}

func TestRingBufferDefaultSize(t *testing.T) {
	rb := NewRingBuffer(0)
	if rb.size != 256 {
		t.Errorf("expected default size 256, got %d", rb.size)
	}

	rb2 := NewRingBuffer(-1)
	if rb2.size != 256 {
		t.Errorf("expected default size 256 for negative, got %d", rb2.size)
	}
}

func TestRingBufferEventsSinceEmpty(t *testing.T) {
	rb := NewRingBuffer(5)

	events := rb.EventsSince("")
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty buffer, got %d", len(events))
	}

	events = rb.EventsSince("123")
	if len(events) != 0 {
		t.Errorf("expected 0 events for empty buffer with ID, got %d", len(events))
	}
}
