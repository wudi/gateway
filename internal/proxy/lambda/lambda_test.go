package lambda

import (
	"net/http"
	"testing"

	"github.com/wudi/runway/config"
)

func TestLambdaHandlerValidation(t *testing.T) {
	_, err := New(config.LambdaConfig{})
	if err == nil {
		t.Error("expected error for empty function_name")
	}
}

func TestLambdaByRoute(t *testing.T) {
	m := NewLambdaByRoute()
	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats map")
	}
}

func TestFlattenHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-Custom", "value")
	h.Add("Accept", "text/html")

	flat := flattenHeaders(h)

	if flat["Content-Type"] != "application/json" {
		t.Errorf("Content-Type = %q, want %q", flat["Content-Type"], "application/json")
	}
	if flat["X-Custom"] != "value" {
		t.Errorf("X-Custom = %q, want %q", flat["X-Custom"], "value")
	}
	if flat["Accept"] != "text/html" {
		t.Errorf("Accept = %q, want %q", flat["Accept"], "text/html")
	}
}

func TestFlattenHeadersEmpty(t *testing.T) {
	flat := flattenHeaders(http.Header{})
	if len(flat) != 0 {
		t.Errorf("expected empty map, got %d entries", len(flat))
	}
}

func TestHandlerStats(t *testing.T) {
	h := &Handler{
		functionName: "my-function",
	}

	stats := h.Stats()
	if stats["function_name"] != "my-function" {
		t.Errorf("function_name = %v, want %q", stats["function_name"], "my-function")
	}
	if stats["total_requests"].(int64) != 0 {
		t.Errorf("total_requests = %v, want 0", stats["total_requests"])
	}
	if stats["total_errors"].(int64) != 0 {
		t.Errorf("total_errors = %v, want 0", stats["total_errors"])
	}
	if stats["total_invokes"].(int64) != 0 {
		t.Errorf("total_invokes = %v, want 0", stats["total_invokes"])
	}
}

func TestLambdaByRouteGetHandlerNonexistent(t *testing.T) {
	m := NewLambdaByRoute()
	h := m.GetHandler("nonexistent")
	if h != nil {
		t.Error("GetHandler should return nil for nonexistent route")
	}
}
