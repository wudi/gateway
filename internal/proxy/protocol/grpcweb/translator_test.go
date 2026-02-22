package grpcweb

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/proxy/protocol"
)

func TestTranslatorName(t *testing.T) {
	tr := New()
	if tr.Name() != "grpc_web" {
		t.Errorf("Name() = %q, want %q", tr.Name(), "grpc_web")
	}
}

func TestParseGRPCWebPath(t *testing.T) {
	tests := []struct {
		path    string
		service string
		method  string
		wantErr bool
	}{
		{"/pkg.Service/Method", "pkg.Service", "Method", false},
		{"/com.example.api.UserService/GetUser", "com.example.api.UserService", "GetUser", false},
		{"/Service/Method", "Service", "Method", false},
		{"", "", "", true},
		{"/", "", "", true},
		{"/OnlyService", "", "", true},
		{"//Method", "", "", true},
		{"/Service/", "", "", true},
	}

	for _, tt := range tests {
		svc, meth, err := parseGRPCWebPath(tt.path)
		if (err != nil) != tt.wantErr {
			t.Errorf("parseGRPCWebPath(%q): err = %v, wantErr = %v", tt.path, err, tt.wantErr)
			continue
		}
		if !tt.wantErr {
			if svc != tt.service {
				t.Errorf("parseGRPCWebPath(%q): service = %q, want %q", tt.path, svc, tt.service)
			}
			if meth != tt.method {
				t.Errorf("parseGRPCWebPath(%q): method = %q, want %q", tt.path, meth, tt.method)
			}
		}
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
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}

	// Create a mock balancer that returns no backends.
	bal := &mockBalancer{}

	_, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	m := tr.Metrics("test-route")
	if m == nil {
		t.Fatal("expected metrics for test-route, got nil")
	}
	if m.ProtocolType != "grpc_web" {
		t.Errorf("ProtocolType = %q, want %q", m.ProtocolType, "grpc_web")
	}
	if m.Requests != 0 {
		t.Errorf("Requests = %d, want 0", m.Requests)
	}
}

func TestTranslatorClose(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
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
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("GET", "/pkg.Service/Method", nil)
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 405 {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestTranslatorRejectsInvalidContentType(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	req := httptest.NewRequest("POST", "/pkg.Service/Method", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestTranslatorRejectsInvalidPath(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	body := encodeDataFrame([]byte("test"))
	req := httptest.NewRequest("POST", "/invalid-path", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestTranslatorNoBackend(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}
	// Balancer that returns nil (no backends).
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	body := encodeDataFrame([]byte("test"))
	req := httptest.NewRequest("POST", "/pkg.Service/Method", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestTranslatorTextModeDisabled(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout:  5 * time.Second,
			TextMode: false,
		},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	body := encodeDataFrame([]byte("test"))
	encodedBody := base64Encode(body)
	req := httptest.NewRequest("POST", "/pkg.Service/Method", bytes.NewReader(encodedBody))
	req.Header.Set("Content-Type", "application/grpc-web-text+proto")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("status = %d, want 400 (text mode disabled)", w.Code)
	}
}

func TestTranslatorMetricsIncrementOnFailure(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	// Send a request that will fail (no backend).
	body := encodeDataFrame([]byte("test"))
	req := httptest.NewRequest("POST", "/pkg.Service/Method", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc-web+proto")
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

func TestExtractMetadata(t *testing.T) {
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Custom-Header", "value1")
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/grpc-web") // should be skipped
	req.Header.Set("User-Agent", "test")                   // should be skipped

	md := extractMetadata(req)
	if v := md.Get("x-custom-header"); len(v) == 0 || v[0] != "value1" {
		t.Errorf("x-custom-header: got %v, want [value1]", v)
	}
	if v := md.Get("authorization"); len(v) == 0 || v[0] != "Bearer token" {
		t.Errorf("authorization: got %v, want [Bearer token]", v)
	}
	if v := md.Get("content-type"); len(v) > 0 {
		t.Errorf("content-type should be skipped, got %v", v)
	}
	if v := md.Get("user-agent"); len(v) > 0 {
		t.Errorf("user-agent should be skipped, got %v", v)
	}
}

func TestRawCodec(t *testing.T) {
	codec := rawCodec{}

	if codec.Name() != "proto" {
		t.Errorf("Name() = %q, want %q", codec.Name(), "proto")
	}

	// Marshal
	data := []byte("hello")
	encoded, err := codec.Marshal(&data)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if !bytes.Equal(encoded, data) {
		t.Errorf("Marshal: got %q, want %q", encoded, data)
	}

	// Unmarshal
	var out []byte
	if err := codec.Unmarshal([]byte("world"), &out); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if !bytes.Equal(out, []byte("world")) {
		t.Errorf("Unmarshal: got %q, want %q", out, "world")
	}

	// Wrong type
	if _, err := codec.Marshal("wrong"); err == nil {
		t.Error("expected error for wrong type in Marshal")
	}
	var s string
	if err := codec.Unmarshal([]byte("test"), &s); err == nil {
		t.Error("expected error for wrong type in Unmarshal")
	}
}

func TestWriteGRPCWebResponse(t *testing.T) {
	tr := New()
	w := httptest.NewRecorder()

	data := []byte("response-data")
	tr.writeGRPCWebResponse(w, false, data, nil, nil)

	resp := w.Result()
	if resp.Header.Get("Content-Type") != "application/grpc-web+proto" {
		t.Errorf("Content-Type = %q, want %q", resp.Header.Get("Content-Type"), "application/grpc-web+proto")
	}

	body, _ := io.ReadAll(resp.Body)

	// Should contain a data frame + trailer frame.
	reader := bytes.NewReader(body)
	f1, err := decodeGRPCWebFrame(reader, 0)
	if err != nil {
		t.Fatalf("decode data frame: %v", err)
	}
	if f1.isTrailer() {
		t.Error("first frame should be data, not trailer")
	}
	if !bytes.Equal(f1.Payload, data) {
		t.Errorf("data frame payload: got %q, want %q", f1.Payload, data)
	}

	f2, err := decodeGRPCWebFrame(reader, 0)
	if err != nil {
		t.Fatalf("decode trailer frame: %v", err)
	}
	if !f2.isTrailer() {
		t.Error("second frame should be trailer")
	}
	trailers := parseTrailerFrame(f2.Payload)
	if trailers["grpc-status"] != "0" {
		t.Errorf("grpc-status: got %q, want %q", trailers["grpc-status"], "0")
	}
}

func TestWriteGRPCWebResponseTextMode(t *testing.T) {
	tr := New()
	w := httptest.NewRecorder()

	data := []byte("response-data")
	tr.writeGRPCWebResponse(w, true, data, nil, nil)

	resp := w.Result()
	if resp.Header.Get("Content-Type") != "application/grpc-web-text+proto" {
		t.Errorf("Content-Type = %q, want %q", resp.Header.Get("Content-Type"), "application/grpc-web-text+proto")
	}

	body, _ := io.ReadAll(resp.Body)

	// Body should be two base64-encoded frames concatenated.
	// Decode the data frame (first base64 chunk).
	dataFrame := encodeDataFrame(data)
	expectedDataB64 := base64Encode(dataFrame)

	if !bytes.HasPrefix(body, expectedDataB64) {
		t.Error("text mode response should start with base64-encoded data frame")
	}
}

func TestWriteGRPCWebError(t *testing.T) {
	tr := New()
	w := httptest.NewRecorder()

	tr.writeGRPCWebError(w, false, "14", "unavailable")

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)

	// Should be a single trailer frame.
	f, err := decodeGRPCWebFrame(bytes.NewReader(body), 0)
	if err != nil {
		t.Fatalf("decode trailer: %v", err)
	}
	if !f.isTrailer() {
		t.Error("error response should be trailer-only")
	}
	trailers := parseTrailerFrame(f.Payload)
	if trailers["grpc-status"] != "14" {
		t.Errorf("grpc-status: got %q, want %q", trailers["grpc-status"], "14")
	}
	if trailers["grpc-message"] != "unavailable" {
		t.Errorf("grpc-message: got %q, want %q", trailers["grpc-message"], "unavailable")
	}
}

func TestProtocolRegistration(t *testing.T) {
	// Verify that init() registered the "grpc_web" translator.
	tr, err := protocol.New("grpc_web")
	if err != nil {
		t.Fatalf("protocol.New(grpc_web) failed: %v", err)
	}
	if tr.Name() != "grpc_web" {
		t.Errorf("Name() = %q, want %q", tr.Name(), "grpc_web")
	}
}

// mockBalancer implements loadbalancer.Balancer with no backends.
type mockBalancer struct {
	backend *loadbalancer.Backend
}

func (m *mockBalancer) Next() *loadbalancer.Backend {
	return m.backend
}

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
