package lambda

import (
	"testing"

	"github.com/wudi/gateway/config"
)

func TestLambdaHandlerValidation(t *testing.T) {
	_, err := New(config.LambdaConfig{})
	if err == nil {
		t.Error("expected error for empty function_name")
	}
}

func TestLambdaByRoute(t *testing.T) {
	m := NewLambdaByRoute()
	// Can't actually invoke Lambda in unit tests without AWS credentials,
	// but we can test the manager structure
	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats map")
	}
}
