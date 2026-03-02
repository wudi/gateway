//go:build integration

package test

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/runway"
	"google.golang.org/grpc"

	// Register the grpc_json translator.
	_ "github.com/wudi/runway/internal/proxy/protocol/grpcjson"
)

// jsonCodec is a gRPC codec that passes raw JSON bytes through.
type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error)      { return *v.(*[]byte), nil }
func (jsonCodec) Unmarshal(data []byte, v interface{}) error  { *v.(*[]byte) = append((*v.(*[]byte))[:0], data...); return nil }
func (jsonCodec) Name() string                                { return "json" }

func TestGRPCJSONTranslatorConfig(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18300",
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
			ID:         "grpcjson-test",
			Path:       "/grpcjson/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50070",
			}},
			Protocol: config.ProtocolConfig{
				Type: "grpc_json",
				GRPCJson: config.GRPCJSONTranslateConfig{
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    18301,
		},
	}

	gw, err := runway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runway: %v", err)
	}
	defer gw.Close()

	// Verify translator is registered.
	translators := gw.GetTranslators()
	if !translators.HasRoute("grpcjson-test") {
		t.Error("Expected translator to be registered for grpcjson-test route")
	}

	stats := translators.Stats()
	if _, ok := stats["grpcjson-test"]; !ok {
		t.Error("Expected stats to include grpcjson-test route")
	}
}

func TestGRPCJSONTranslatorAdminEndpoint(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18302",
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
			ID:         "grpcjson-admin-test",
			Path:       "/grpcjson/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://localhost:50071",
			}},
			Protocol: config.ProtocolConfig{
				Type: "grpc_json",
				GRPCJson: config.GRPCJSONTranslateConfig{
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: true,
			Port:    18303,
		},
	}

	server, err := runway.NewServer(cfg, "")
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Shutdown(5 * time.Second)

	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://localhost:18303/protocol-translators")
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

	if _, ok := stats["grpcjson-admin-test"]; !ok {
		t.Errorf("Expected grpcjson-admin-test in stats, got %v", stats)
	}
}

func TestGRPCJSONTranslatorFullRoundTrip(t *testing.T) {
	// Start a real gRPC server with JSON codec.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	grpcSrv := grpc.NewServer(grpc.ForceServerCodec(jsonCodec{}))
	grpcSrv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "test.EchoService",
		HandlerType: (*interface{})(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Echo",
				Handler: func(srv interface{}, ctx interface{}, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
					var reqBody []byte
					if err := dec(&reqBody); err != nil {
						return nil, err
					}
					resp := []byte(`{"echo":` + string(reqBody) + `}`)
					return &resp, nil
				},
			},
		},
		Metadata: "",
	}, &struct{}{})

	go grpcSrv.Serve(lis)
	defer grpcSrv.Stop()

	backendAddr := lis.Addr().String()

	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18304",
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
			ID:         "grpcjson-roundtrip",
			Path:       "/test.EchoService",
			PathPrefix: true,
			Backends: []config.BackendConfig{{
				URL: "grpc://" + backendAddr,
			}},
			Protocol: config.ProtocolConfig{
				Type: "grpc_json",
				GRPCJson: config.GRPCJSONTranslateConfig{
					Timeout: 5 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := runway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	reqBody := []byte(`{"message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/test.EchoService/Echo", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if _, ok := resp["echo"]; !ok {
		t.Errorf("Expected 'echo' key in response, got %s", rec.Body.String())
	}
}

func TestGRPCJSONTranslatorNoBackend(t *testing.T) {
	cfg := &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "http-main",
			Address:  ":18306",
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
			ID:       "grpcjson-no-backend",
			Path:     "/grpcjson/*method",
			PathPrefix: true,
			Backends: []config.BackendConfig{},
			Service: config.ServiceConfig{
				Name: "nonexistent-grpcjson-service",
			},
			Protocol: config.ProtocolConfig{
				Type: "grpc_json",
				GRPCJson: config.GRPCJSONTranslateConfig{
					Timeout: 1 * time.Second,
				},
			},
		}},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}

	gw, err := runway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create runway: %v", err)
	}
	defer gw.Close()

	handler := gw.Handler()

	req := httptest.NewRequest(http.MethodPost, "/grpcjson/pkg.Service/Method", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected 503 for no backends, got %d", rec.Code)
	}
}
