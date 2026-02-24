package protocol

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

func TestTranslatorByRoute(t *testing.T) {
	// Register a mock translator
	factoriesMu.Lock()
	delete(factories, "mock_grpc")
	factoriesMu.Unlock()

	Register("mock_grpc", func() Translator {
		return &mockTranslator{
			name: "mock_grpc",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"result":"ok"}`))
			}),
		}
	})

	manager := NewTranslatorByRoute()

	// Create a mock balancer
	balancer := loadbalancer.NewRoundRobin([]*loadbalancer.Backend{
		{URL: "http://localhost:50051", Weight: 1, Healthy: true},
	})

	cfg := config.ProtocolConfig{Type: "mock_grpc"}

	// Add a route
	err := manager.AddRoute("test-route", cfg, balancer)
	if err != nil {
		t.Fatalf("AddRoute failed: %v", err)
	}

	// Verify route exists
	if !manager.HasRoute("test-route") {
		t.Error("expected route to exist")
	}

	// Get handler
	handler := manager.GetHandler("test-route")
	if handler == nil {
		t.Fatal("expected handler to be non-nil")
	}

	// Test handler
	req := httptest.NewRequest(http.MethodPost, "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// Check stats
	stats := manager.Stats()
	if _, ok := stats["test-route"]; !ok {
		t.Error("expected stats for test-route")
	}

	// Check route IDs
	ids := manager.RouteIDs()
	found := false
	for _, id := range ids {
		if id == "test-route" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected test-route in RouteIDs")
	}

	// Close
	manager.Close()

	// After close, route should be gone
	if manager.HasRoute("test-route") {
		t.Error("expected route to be removed after Close")
	}
}

func TestTranslatorByRouteUnknownProtocol(t *testing.T) {
	manager := NewTranslatorByRoute()
	balancer := loadbalancer.NewRoundRobin(nil)

	cfg := config.ProtocolConfig{Type: "nonexistent_protocol"}

	err := manager.AddRoute("bad-route", cfg, balancer)
	if err == nil {
		t.Error("expected error for unknown protocol type")
	}
}

func TestTranslatorByRouteGetHandlerNonexistent(t *testing.T) {
	manager := NewTranslatorByRoute()

	handler := manager.GetHandler("nonexistent-route")
	if handler != nil {
		t.Error("expected nil handler for nonexistent route")
	}
}
