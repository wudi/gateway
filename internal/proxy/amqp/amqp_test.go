package amqp

import (
	"testing"

	"github.com/wudi/runway/config"
)

func TestAMQPHandlerValidation(t *testing.T) {
	_, err := New(config.AMQPConfig{})
	if err == nil {
		t.Error("expected error for empty url")
	}
}

func TestAMQPByRoute(t *testing.T) {
	m := NewAMQPByRoute()
	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats map")
	}
}

func TestHandlerStats(t *testing.T) {
	h := &Handler{
		url: "amqp://localhost:5672",
	}

	stats := h.Stats()
	if stats["url"] != "amqp://localhost:5672" {
		t.Errorf("url = %v, want %q", stats["url"], "amqp://localhost:5672")
	}
	if stats["total_requests"].(int64) != 0 {
		t.Errorf("total_requests = %v, want 0", stats["total_requests"])
	}
	if stats["total_errors"].(int64) != 0 {
		t.Errorf("total_errors = %v, want 0", stats["total_errors"])
	}
	if stats["published"].(int64) != 0 {
		t.Errorf("published = %v, want 0", stats["published"])
	}
	if stats["consumed"].(int64) != 0 {
		t.Errorf("consumed = %v, want 0", stats["consumed"])
	}
}

func TestAMQPByRouteGetHandlerMissing(t *testing.T) {
	m := NewAMQPByRoute()
	if m.GetHandler("missing") != nil {
		t.Error("GetHandler should return nil for nonexistent route")
	}
}

func TestAMQPByRouteAddRouteError(t *testing.T) {
	m := NewAMQPByRoute()
	// Empty URL should fail validation
	err := m.AddRoute("r1", config.AMQPConfig{})
	if err == nil {
		t.Error("AddRoute with empty URL should error")
	}
}

func TestAMQPByRouteGetHandlerNonexistent(t *testing.T) {
	m := NewAMQPByRoute()
	h := m.GetHandler("nonexistent")
	if h != nil {
		t.Error("GetHandler should return nil for nonexistent route")
	}
}

func TestCloseNilConnAndChannel(t *testing.T) {
	h := &Handler{}
	err := h.Close()
	if err != nil {
		t.Errorf("Close with nil conn/channel should not error: %v", err)
	}
}
