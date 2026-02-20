package rest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
)

func TestTranslatorName(t *testing.T) {
	tr := New()
	if tr.Name() != "grpc_to_rest" {
		t.Errorf("expected name 'grpc_to_rest', got %q", tr.Name())
	}
}

func TestTranslatorHandler(t *testing.T) {
	// Set up a REST backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/123" {
			t.Errorf("expected path /users/123, got %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"user_id": "123",
			"name":    "Test User",
		})
	}))
	defer backend.Close()

	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_to_rest",
		REST: config.RESTTranslateConfig{
			Mappings: []config.GRPCToRESTMapping{
				{
					GRPCService: "users.UserService",
					GRPCMethod:  "GetUser",
					HTTPMethod:  "GET",
					HTTPPath:    "/users/{user_id}",
					Body:        "",
				},
			},
		},
	}

	backends := []*loadbalancer.Backend{
		{URL: backend.URL, Weight: 1, Healthy: true},
	}
	bal := loadbalancer.NewRoundRobin(backends)

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	// Build gRPC request with JSON body (no descriptor files → raw JSON mode)
	reqBody := []byte(`{"user_id":"123"}`)
	var grpcBody bytes.Buffer
	encodeGRPCFrame(&grpcBody, reqBody, false)

	r := httptest.NewRequest("POST", "/users.UserService/GetUser", &grpcBody)
	r.Header.Set("Content-Type", "application/grpc")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != 200 {
		t.Errorf("expected HTTP 200, got %d", rec.Code)
	}

	grpcStatus := rec.Header().Get("Grpc-Status")
	if grpcStatus != "0" {
		t.Errorf("expected Grpc-Status 0, got %q (message: %s)",
			grpcStatus, rec.Header().Get("Grpc-Message"))
	}
}

func TestTranslatorUnmappedMethod(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_to_rest",
		REST: config.RESTTranslateConfig{
			Mappings: []config.GRPCToRESTMapping{
				{
					GRPCService: "users.UserService",
					GRPCMethod:  "GetUser",
					HTTPMethod:  "GET",
					HTTPPath:    "/users/{user_id}",
				},
			},
		},
	}

	backends := []*loadbalancer.Backend{{URL: "http://localhost:1", Weight: 1, Healthy: true}}
	bal := loadbalancer.NewRoundRobin(backends)

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	var grpcBody bytes.Buffer
	encodeGRPCFrame(&grpcBody, []byte(`{}`), false)

	r := httptest.NewRequest("POST", "/users.UserService/UnknownMethod", &grpcBody)
	r.Header.Set("Content-Type", "application/grpc")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	grpcStatus := rec.Header().Get("Grpc-Status")
	if grpcStatus != "12" { // UNIMPLEMENTED
		t.Errorf("expected Grpc-Status 12 (UNIMPLEMENTED), got %q", grpcStatus)
	}
}

func TestTranslatorBackendError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer backend.Close()

	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_to_rest",
		REST: config.RESTTranslateConfig{
			Mappings: []config.GRPCToRESTMapping{
				{
					GRPCService: "users.UserService",
					GRPCMethod:  "GetUser",
					HTTPMethod:  "GET",
					HTTPPath:    "/users/{user_id}",
				},
			},
		},
	}

	backends := []*loadbalancer.Backend{{URL: backend.URL, Weight: 1, Healthy: true}}
	bal := loadbalancer.NewRoundRobin(backends)

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("failed to create handler: %v", err)
	}

	var grpcBody bytes.Buffer
	encodeGRPCFrame(&grpcBody, []byte(`{"user_id":"999"}`), false)

	r := httptest.NewRequest("POST", "/users.UserService/GetUser", &grpcBody)
	r.Header.Set("Content-Type", "application/grpc")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	grpcStatus := rec.Header().Get("Grpc-Status")
	if grpcStatus != "5" { // NOT_FOUND
		t.Errorf("expected Grpc-Status 5 (NOT_FOUND), got %q", grpcStatus)
	}
}

func TestTranslatorMetrics(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_to_rest",
		REST: config.RESTTranslateConfig{
			Mappings: []config.GRPCToRESTMapping{
				{
					GRPCService: "test.Svc",
					GRPCMethod:  "Do",
					HTTPMethod:  "POST",
					HTTPPath:    "/do",
					Body:        "*",
				},
			},
		},
	}

	backends := []*loadbalancer.Backend{{URL: backend.URL, Weight: 1, Healthy: true}}
	bal := loadbalancer.NewRoundRobin(backends)

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatal(err)
	}

	var grpcBody bytes.Buffer
	encodeGRPCFrame(&grpcBody, []byte(`{"key":"value"}`), false)

	r := httptest.NewRequest("POST", "/test.Svc/Do", &grpcBody)
	r.Header.Set("Content-Type", "application/grpc")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	metrics := tr.Metrics("test-route")
	if metrics == nil {
		t.Fatal("expected metrics")
	}
	if metrics.Requests != 1 {
		t.Errorf("expected 1 request, got %d", metrics.Requests)
	}
	if metrics.Successes != 1 {
		t.Errorf("expected 1 success, got %d", metrics.Successes)
	}
	if metrics.ProtocolType != "grpc_to_rest" {
		t.Errorf("expected protocol type 'grpc_to_rest', got %q", metrics.ProtocolType)
	}
}

func TestTranslatorClose(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_to_rest",
		REST: config.RESTTranslateConfig{
			Mappings: []config.GRPCToRESTMapping{
				{GRPCService: "s", GRPCMethod: "m", HTTPMethod: "GET", HTTPPath: "/"},
			},
		},
	}

	backends := []*loadbalancer.Backend{{URL: "http://localhost:1", Weight: 1, Healthy: true}}
	bal := loadbalancer.NewRoundRobin(backends)
	tr.Handler("test-route", bal, cfg)

	if err := tr.Close("test-route"); err != nil {
		t.Fatal(err)
	}
	if tr.Metrics("test-route") != nil {
		t.Error("expected nil metrics after close")
	}
}

func TestHTTPToGRPCStatus(t *testing.T) {
	tests := []struct {
		http int
		grpc int
	}{
		{200, 0},
		{201, 0},
		{400, 3},
		{401, 16},
		{403, 7},
		{404, 5},
		{409, 6},
		{429, 8},
		{500, 13},
		{501, 12},
		{503, 14},
		{504, 4},
	}

	for _, tt := range tests {
		got := httpToGRPCStatus(tt.http)
		if got != tt.grpc {
			t.Errorf("httpToGRPCStatus(%d) = %d, want %d", tt.http, got, tt.grpc)
		}
	}
}

// Helper to create gRPC frame for testing
func makeGRPCFrame(data []byte) io.Reader {
	buf := make([]byte, 5+len(data))
	buf[0] = 0 // not compressed
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(data)))
	copy(buf[5:], data)
	return bytes.NewReader(buf)
}
