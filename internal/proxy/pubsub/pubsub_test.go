package pubsub

import (
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestPubSubHandlerValidation(t *testing.T) {
	_, err := New(config.PubSubConfig{})
	if err == nil {
		t.Error("expected error for empty config")
	}
}

func TestPubSubByRoute(t *testing.T) {
	m := NewPubSubByRoute()
	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats map")
	}
}
