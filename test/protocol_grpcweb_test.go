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

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/gateway"

	// Register the grpc_web translator
	_ "github.com/wudi/gateway/internal/proxy/protocol/grpcweb"
)

func TestGRPCWebTranslatorConfig(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18200",
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
			ID:         "grpcweb-test",
			Path:       "/grpcweb/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50060",
			}},
			Protocol: config.ProtocolConfig{
				Type: "grpc_web",
				GRPCWeb: config.GRPCWebTranslateConfig{
					Timeout:        5 * time.Second,
					MaxMessageSize: 4 * 1024 * 1024,
					TextMode:       true,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    18201,
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	// Verify translator is registered.
	translators := gw.GetTranslators()
	if !translators.HasRoute("grpcweb-test") {
		t.Error("Expected translator to be registered for grpcweb-test route")
	}

	stats := translators.Stats()
	if _, ok := stats["grpcweb-test"]; !ok {
		t.Error("Expected stats to include grpcweb-test route")
	}
}

func TestGRPCWebTranslatorAdminEndpoint(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18202",
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
			ID:         "grpcweb-admin-test",
			Path:       "/grpcweb/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50061",
			}},
			Protocol: config.ProtocolConfig{
				Type: "grpc_web",
				GRPCWeb: config.GRPCWebTranslateConfig{
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    18203,
		},
	}

	server, err := gateway.NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:18203/protocol-translators")
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

	if _, ok := stats["grpcweb-admin-test"]; !ok {
		t.Errorf("Expected grpcweb-admin-test in stats, got %v", stats)
	}
}

func TestGRPCWebTranslatorRejectsNonGRPCWebContentType(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18204",
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
			ID:         "grpcweb-ct-test",
			Path:       "/grpcweb/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50062",
			}},
			Protocol: config.ProtocolConfig{
				Type: "grpc_web",
				GRPCWeb: config.GRPCWebTranslateConfig{
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

	// JSON content type should be rejected.
	req := httptest.NewRequest(http.MethodPost, "/grpcweb/pkg.Service/Method", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for non-grpc-web content type, got %d", rec.Code)
	}
}

func TestGRPCWebTranslatorNoBackend(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18206",
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
			ID:       "grpcweb-no-backend",
			Path:     "/grpcweb/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{},
			Service: config.ServiceConfig{
				Name: "nonexistent-grpcweb-service",
			},
			Protocol: config.ProtocolConfig{
				Type: "grpc_web",
				GRPCWeb: config.GRPCWebTranslateConfig{
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

	// Build a proper gRPC-Web frame.
	frame := make([]byte, 5+4)
	frame[0] = 0x00 // data flag
	frame[1] = 0x00
	frame[2] = 0x00
	frame[3] = 0x00
	frame[4] = 0x04 // 4 bytes payload
	copy(frame[5:], []byte("test"))

	req := httptest.NewRequest(http.MethodPost, "/grpcweb/pkg.Service/Method", bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 for no backends, got %d", rec.Code)
	}
}
