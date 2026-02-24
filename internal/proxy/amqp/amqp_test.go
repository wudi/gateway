package amqp

import (
	"testing"

	"github.com/wudi/gateway/config"
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
