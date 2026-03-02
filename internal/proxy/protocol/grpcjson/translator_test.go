package grpcjson

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/proxy/protocol"
	"google.golang.org/grpc"
)

func TestJsonCodecName(t *testing.T) {
	c := jsonCodec{}
	if c.Name() != "json" {
		t.Errorf("Name() = %q, want %q", c.Name(), "json")
	}
}

func TestJsonCodecMarshalUnmarshal(t *testing.T) {
	c := jsonCodec{}

	// Marshal
	data := []byte(`{"hello":"world"}`)
	encoded, err := c.Marshal(&data)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if !bytes.Equal(encoded, data) {
		t.Errorf("Marshal: got %q, want %q", encoded, data)
	}

	// Unmarshal
	var out []byte
	if err := c.Unmarshal([]byte(`{"foo":"bar"}`), &out); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !bytes.Equal(out, []byte(`{"foo":"bar"}`)) {
		t.Errorf("Unmarshal: got %q, want %q", out, `{"foo":"bar"}`)
	}
}

func TestJsonCodecTypeError(t *testing.T) {
	c := jsonCodec{}

	// Marshal wrong type
	if _, err := c.Marshal("wrong"); err == nil {
		t.Error("expected error for wrong type in Marshal")
	}

	// Unmarshal wrong type
	var s string
	if err := c.Unmarshal([]byte("test"), &s); err == nil {
		t.Error("expected error for wrong type in Unmarshal")
	}
}

func TestResolveMethod(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		cfg     config.GRPCJSONTranslateConfig
		want    string
		wantErr bool
	}{
		{
			name: "fixed mode",
			path: "/anything",
			cfg:  config.GRPCJSONTranslateConfig{Service: "mypackage.MyService", Method: "GetUser"},
			want: "/mypackage.MyService/GetUser",
		},
		{
			name: "service-scoped mode",
			path: "/api/myservice/GetUser",
			cfg:  config.GRPCJSONTranslateConfig{Service: "mypackage.MyService"},
			want: "/mypackage.MyService/GetUser",
		},
		{
			name: "service-scoped with trailing slash",
			path: "/api/myservice/ListUsers",
			cfg:  config.GRPCJSONTranslateConfig{Service: "pkg.Svc"},
			want: "/pkg.Svc/ListUsers",
		},
		{
			name: "path-based mode",
			path: "/mypackage.MyService/GetUser",
			cfg:  config.GRPCJSONTranslateConfig{},
			want: "/mypackage.MyService/GetUser",
		},
		{
			name: "path-based with nested package",
			path: "/com.example.api.UserService/CreateUser",
			cfg:  config.GRPCJSONTranslateConfig{},
			want: "/com.example.api.UserService/CreateUser",
		},
		{
			name:    "empty path in path-based mode",
			path:    "/",
			cfg:     config.GRPCJSONTranslateConfig{},
			wantErr: true,
		},
		{
			name:    "missing method in path-based mode",
			path:    "/OnlyService",
			cfg:     config.GRPCJSONTranslateConfig{},
			wantErr: true,
		},
		{
			name:    "empty service in path-based mode",
			path:    "//Method",
			cfg:     config.GRPCJSONTranslateConfig{},
			wantErr: true,
		},
		{
			name:    "empty method in path-based mode",
			path:    "/Service/",
			cfg:     config.GRPCJSONTranslateConfig{},
			wantErr: true,
		},
		{
			name:    "service-scoped with empty path",
			path:    "/",
			cfg:     config.GRPCJSONTranslateConfig{Service: "pkg.Svc"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveMethod(tt.path, tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveMethod() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("resolveMethod() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractMetadata(t *testing.T) {
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Custom-Header", "value1")
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json") // should be skipped
	req.Header.Set("Accept-Encoding", "gzip")          // should be skipped
	req.Header.Set("User-Agent", "test")                // should be skipped
	req.Header.Set("Host", "example.com")               // should be skipped

	md := extractMetadata(req)
	if v := md.Get("x-custom-header"); len(v) == 0 || v[0] != "value1" {
		t.Errorf("x-custom-header: got %v, want [value1]", v)
	}
	if v := md.Get("authorization"); len(v) == 0 || v[0] != "Bearer token" {
		t.Errorf("authorization: got %v, want [Bearer token]", v)
	}
	for _, skip := range []string{"content-type", "accept-encoding", "user-agent", "host"} {
		if v := md.Get(skip); len(v) > 0 {
			t.Errorf("%s should be skipped, got %v", skip, v)
		}
	}
}

func TestTranslatorName(t *testing.T) {
	tr := New()
	if tr.Name() != "grpc_json" {
		t.Errorf("Name() = %q, want %q", tr.Name(), "grpc_json")
	}
}

func TestTranslatorMetricsNonexistentRoute(t *testing.T) {
	tr := New()
	if m := tr.Metrics("nonexistent"); m != nil {
		t.Errorf("expected nil metrics for nonexistent route, got %+v", m)
	}
}

func TestTranslatorMetricsAfterHandler(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}

	bal := &mockBalancer{}
	_, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	m := tr.Metrics("test-route")
	if m == nil {
		t.Fatal("expected metrics for test-route, got nil")
	}
	if m.ProtocolType != "grpc_json" {
		t.Errorf("ProtocolType = %q, want %q", m.ProtocolType, "grpc_json")
	}
	if m.Requests != 0 {
		t.Errorf("Requests = %d, want 0", m.Requests)
	}
}

func TestTranslatorClose(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}
	bal := &mockBalancer{}

	_, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	if err := tr.Close("test-route"); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if m := tr.Metrics("test-route"); m != nil {
		t.Errorf("expected nil metrics after close, got %+v", m)
	}
}

func TestTranslatorRejectsNonPOST(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/pkg.Service/Method", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestTranslatorRejectsInvalidPath(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("POST", "/invalid-path", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestTranslatorNoBackend(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("POST", "/pkg.Service/Method", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestTranslatorMetricsIncrementOnFailure(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("POST", "/pkg.Service/Method", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	m := tr.Metrics("test-route")
	if m.Requests != 1 {
		t.Errorf("Requests = %d, want 1", m.Requests)
	}
	if m.Failures != 1 {
		t.Errorf("Failures = %d, want 1", m.Failures)
	}
}

func TestProtocolRegistration(t *testing.T) {
	tr, err := protocol.New("grpc_json")
	if err != nil {
		t.Fatalf("protocol.New(grpc_json) failed: %v", err)
	}
	if tr.Name() != "grpc_json" {
		t.Errorf("Name() = %q, want %q", tr.Name(), "grpc_json")
	}
}

func TestTranslatorDefaultTimeout(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{}, // zero timeout → should default to 30s
	}
	bal := &mockBalancer{}

	// Should not error even with zero timeout.
	_, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}
}

func TestTranslatorWriteError(t *testing.T) {
	tr := New()
	w := httptest.NewRecorder()

	tr.writeError(w, 5, "not found")

	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var errResp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Details []any  `json:"details"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to parse error response: %v", err)
	}
	if errResp.Code != 5 {
		t.Errorf("error code = %d, want 5", errResp.Code)
	}
	if errResp.Message != "not found" {
		t.Errorf("error message = %q, want %q", errResp.Message, "not found")
	}
}

// TestTranslatorFullRoundTrip tests the full request flow with a real gRPC server
// that registers a JSON codec.
func TestTranslatorFullRoundTrip(t *testing.T) {
	// Start a gRPC server with JSON codec.
	grpcSrv, addr := startMockJSONGRPCServer(t)
	defer grpcSrv.Stop()

	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}
	bal := &mockBalancer{
		backend: &loadbalancer.Backend{URL: addr},
	}

	handler, err := tr.Handler("roundtrip-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	reqBody := []byte(`{"name":"Alice"}`)
	req := httptest.NewRequest("POST", "/mock.EchoService/Echo", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		body, _ := io.ReadAll(w.Result().Body)
		t.Fatalf("status = %d, want 200; body = %s", w.Code, body)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	// The mock server echoes back the request as {"echo": <request>}.
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if _, ok := resp["echo"]; !ok {
		t.Errorf("response missing 'echo' key, got %s", w.Body.String())
	}

	// Check metrics.
	m := tr.Metrics("roundtrip-route")
	if m.Requests != 1 {
		t.Errorf("Requests = %d, want 1", m.Requests)
	}
	if m.Successes != 1 {
		t.Errorf("Successes = %d, want 1", m.Successes)
	}
}

func TestTranslatorFullRoundTripFixedMethod(t *testing.T) {
	grpcSrv, addr := startMockJSONGRPCServer(t)
	defer grpcSrv.Stop()

	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{
			Service: "mock.EchoService",
			Method:  "Echo",
			Timeout: 5 * time.Second,
		},
	}
	bal := &mockBalancer{
		backend: &loadbalancer.Backend{URL: addr},
	}

	handler, err := tr.Handler("fixed-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	reqBody := []byte(`{"name":"Bob"}`)
	req := httptest.NewRequest("POST", "/any-path", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

func TestTranslatorFullRoundTripServiceScoped(t *testing.T) {
	grpcSrv, addr := startMockJSONGRPCServer(t)
	defer grpcSrv.Stop()

	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{
			Service: "mock.EchoService",
			Timeout: 5 * time.Second,
		},
	}
	bal := &mockBalancer{
		backend: &loadbalancer.Backend{URL: addr},
	}

	handler, err := tr.Handler("scoped-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	reqBody := []byte(`{"name":"Charlie"}`)
	// URL path last segment "Echo" becomes the method name.
	req := httptest.NewRequest("POST", "/api/myservice/Echo", bytes.NewReader(reqBody))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
}

func TestTranslatorCloseAll(t *testing.T) {
	tr := New()
	// Should not panic even with no connections.
	tr.CloseAll()
}

func TestTranslatorHeaderForwarding(t *testing.T) {
	grpcSrv, addr := startMockJSONGRPCServer(t)
	defer grpcSrv.Stop()

	tr := New()
	cfg := config.ProtocolConfig{
		Type:     "grpc_json",
		GRPCJson: config.GRPCJSONTranslateConfig{Timeout: 5 * time.Second},
	}
	bal := &mockBalancer{
		backend: &loadbalancer.Backend{URL: addr},
	}

	handler, err := tr.Handler("header-test", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	reqBody := []byte(`{"name":"test"}`)
	req := httptest.NewRequest("POST", "/mock.EchoService/Echo", bytes.NewReader(reqBody))
	req.Header.Set("X-Request-Id", "test-123")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

// mockBalancer implements loadbalancer.Balancer.
type mockBalancer struct {
	backend *loadbalancer.Backend
}

func (m *mockBalancer) Next() *loadbalancer.Backend          { return m.backend }
func (m *mockBalancer) UpdateBackends([]*loadbalancer.Backend) {}
func (m *mockBalancer) MarkHealthy(string)                     {}
func (m *mockBalancer) MarkUnhealthy(string)                   {}
func (m *mockBalancer) HealthyCount() int                      { return 0 }

func (m *mockBalancer) GetBackends() []*loadbalancer.Backend {
	if m.backend == nil {
		return nil
	}
	return []*loadbalancer.Backend{m.backend}
}

func (m *mockBalancer) GetBackendByURL(url string) *loadbalancer.Backend {
	if m.backend != nil && m.backend.URL == url {
		return m.backend
	}
	return nil
}

// startMockJSONGRPCServer starts a gRPC server with a JSON codec that echoes requests.
func startMockJSONGRPCServer(t *testing.T) (*grpc.Server, string) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := grpc.NewServer(grpc.ForceServerCodec(jsonCodec{}))

	// Register a generic service that echoes the request wrapped in {"echo": ...}.
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "mock.EchoService",
		HandlerType: (*interface{})(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "Echo",
				Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
					var reqBody []byte
					if err := dec(&reqBody); err != nil {
						return nil, err
					}
					// Echo the request inside a wrapper.
					resp := []byte(`{"echo":` + string(reqBody) + `}`)
					return &resp, nil
				},
			},
		},
		Metadata: "",
	}, &struct{}{})

	go srv.Serve(lis)

	return srv, lis.Addr().String()
}
