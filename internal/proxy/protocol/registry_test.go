package protocol

import (
	"net/http"
	"testing"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/loadbalancer"
)

// mockTranslator implements Translator for testing.
type mockTranslator struct {
	name    string
	handler http.Handler
}

func (m *mockTranslator) Name() string { return m.name }

func (m *mockTranslator) Handler(routeID string, balancer loadbalancer.Balancer, cfg config.ProtocolConfig) (http.Handler, error) {
	return m.handler, nil
}

func (m *mockTranslator) Close(routeID string) error { return nil }

func (m *mockTranslator) Metrics(routeID string) *TranslatorMetrics {
	return &TranslatorMetrics{ProtocolType: m.name}
}

func TestRegisterAndNew(t *testing.T) {
	// Clear any existing registrations for clean test
	factoriesMu.Lock()
	delete(factories, "test_protocol")
	factoriesMu.Unlock()

	// Register a test protocol
	Register("test_protocol", func() Translator {
		return &mockTranslator{name: "test_protocol"}
	})

	// Create a new translator
	translator, err := New("test_protocol")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if translator.Name() != "test_protocol" {
		t.Errorf("expected name 'test_protocol', got %q", translator.Name())
	}
}

func TestNewUnknownProtocol(t *testing.T) {
	_, err := New("unknown_protocol")
	if err == nil {
		t.Fatal("expected error for unknown protocol")
	}
}

func TestRegisteredTypes(t *testing.T) {
	// Clear for clean test
	factoriesMu.Lock()
	delete(factories, "type_a")
	delete(factories, "type_b")
	factoriesMu.Unlock()

	Register("type_a", func() Translator { return &mockTranslator{name: "type_a"} })
	Register("type_b", func() Translator { return &mockTranslator{name: "type_b"} })

	types := RegisteredTypes()
	found := make(map[string]bool)
	for _, t := range types {
		found[t] = true
	}

	if !found["type_a"] || !found["type_b"] {
		t.Errorf("expected type_a and type_b in registered types, got %v", types)
	}
}
