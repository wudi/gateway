//go:build integration

package test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/gateway"

	// Register the grpc translator
	_ "github.com/wudi/gateway/internal/proxy/protocol/grpc"
)

func TestProtocolTranslatorConfig(t *testing.T) {
	// Test that the gateway can be configured with a protocol translator
	// (without actually connecting to a gRPC backend)
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18080",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "grpc-test",
			Path:       "/api/grpc/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50051",
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "test.TestService",
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    18081,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	// Verify translator is registered
	translators := gw.GetTranslators()
	if !translators.HasRoute("grpc-test") {
		t.Error("Expected translator to be registered for grpc-test route")
	}

	// Check that stats returns the route
	stats := translators.Stats()
	if _, ok := stats["grpc-test"]; !ok {
		t.Error("Expected stats to include grpc-test route")
	}
}

func TestProtocolTranslatorAdminEndpoint(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18082",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "grpc-admin-test",
			Path:       "/api/grpc/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50052",
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "test.AdminTestService",
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    18083,
		},
	}

	server, err := gateway.NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	// Start the server
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	// Wait for server to start
	time.Sleep(100 * time.Millisecond)

	// Test the admin endpoint
	resp, err := http.Get("http://localhost:18083/protocol-translators")
	if err != nil {
		t.Fatalf("Failed to get protocol-translators: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var stats map[string]interface{}
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if _, ok := stats["grpc-admin-test"]; !ok {
		t.Errorf("Expected grpc-admin-test in stats, got %v", stats)
	}
}

func TestProtocolTranslatorMethodNotAllowed(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18084",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "grpc-method-test",
			Path:       "/api/grpc/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50053",
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "test.MethodTestService",
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	// Test that GET is not allowed (only POST for gRPC translation)
	req := httptest.NewRequest(http.MethodGet, "/api/grpc/GetUser", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for GET request, got %d", rec.Code)
	}

	// Verify error message mentions POST
	body := rec.Body.String()
	if !bytes.Contains([]byte(body), []byte("POST")) {
		t.Errorf("Expected error to mention POST, got: %s", body)
	}
}

func TestProtocolTranslatorFixedMethod(t *testing.T) {
	// Test that fixed method mode accepts any HTTP method
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18086",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:   "grpc-fixed-method",
			Path: "/demo",
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50054",
			}},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "test.FixedMethodService",
					Method:  "GetUser",
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	// Test that GET is allowed in fixed method mode (unlike path-based mode)
	// It will fail to connect to backend, but should not reject the method
	req := httptest.NewRequest(http.MethodGet, "/demo", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Should get 503 (no backend) not 400 (method not allowed)
	if rec.Code == http.StatusBadRequest {
		t.Errorf("Fixed method mode should accept GET, got 400 Bad Request: %s", rec.Body.String())
	}

	// Test POST also works
	req = httptest.NewRequest(http.MethodPost, "/demo", bytes.NewReader([]byte(`{"user_id":"123"}`)))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusBadRequest {
		t.Errorf("Fixed method mode should accept POST, got 400 Bad Request: %s", rec.Body.String())
	}
}

func TestProtocolTranslatorNoBackend(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18085",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{{
			ID:         "grpc-no-backend",
			Path:       "/api/grpc/*method",
			PathPrefix: true,
			Backends:   []config.BackendConfig{}, // Empty backends - will be marked unhealthy
			Service: config.ServiceConfig{
				Name: "nonexistent-service", // Service discovery will find nothing
			},
			Protocol: config.ProtocolConfig{
				Type: "http_to_grpc",
				GRPC: config.GRPCTranslateConfig{
					Service: "test.NoBackendService",
					Timeout: 1 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	// Test that we get 503 when no backends are available
	req := httptest.NewRequest(http.MethodPost, "/api/grpc/GetUser", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503 for no backends, got %d", rec.Code)
	}
}
