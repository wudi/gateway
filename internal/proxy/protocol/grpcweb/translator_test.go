package grpcweb

import (
	"bytes"
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

func TestIsServerStreaming(t *testing.T) {
	tests := []struct {
		name   string
		query  string
		header string
		want   bool
	}{
		{"no signal", "", "", false},
		{"query param", "streaming=server", "", true},
		{"header", "", "server", true},
		{"both", "streaming=server", "server", true},
		{"wrong query value", "streaming=client", "", false},
		{"wrong header value", "", "client", false},
		{"unrelated query", "foo=bar", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/pkg.Service/Method"
			if tt.query != "" {
				url += "?" + tt.query
			}
			req := httptest.NewRequest("POST", url, nil)
			if tt.header != "" {
				req.Header.Set("X-Grpc-Web-Streaming", tt.header)
			}
			if got := isServerStreaming(req); got != tt.want {
				t.Errorf("isServerStreaming() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServeServerStreamNoBackend(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}
	// No backend — balancer returns nil.
	bal := &mockBalancer{}

	handler, err := tr.Handler("test-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	body := encodeDataFrame([]byte("test"))
	req := httptest.NewRequest("POST", "/pkg.Service/Method?streaming=server", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// No backend → 503 with grpc-status 14 (same as unary).
	if w.Code != 503 {
		t.Errorf("status = %d, want 503", w.Code)
	}
	if gs := w.Header().Get("Grpc-Status"); gs != "14" {
		t.Errorf("Grpc-Status = %q, want %q", gs, "14")
	}
}

func TestServerStreamWritesMultipleFrames(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout: 5 * time.Second,
		},
	}

	// Start a minimal gRPC server that streams 3 messages.
	grpcSrv, addr := startMockStreamingGRPCServer(t, [][]byte{
		[]byte("msg1"),
		[]byte("msg2"),
		[]byte("msg3"),
	})
	defer grpcSrv.Stop()

	bal := &mockBalancer{
		backend: &loadbalancer.Backend{URL: addr},
	}

	handler, err := tr.Handler("stream-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	body := encodeDataFrame([]byte("request"))
	req := httptest.NewRequest("POST", "/mock.StreamService/StreamMethod?streaming=server", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc-web+proto")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/grpc-web+proto" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/grpc-web+proto")
	}

	// Decode the response: should be 3 data frames + 1 trailer frame.
	respBody := w.Body.Bytes()
	reader := bytes.NewReader(respBody)

	var dataFrames [][]byte
	var trailerFrame *grpcWebFrame

	for {
		f, err := decodeGRPCWebFrame(reader, 0)
		if err != nil {
			break
		}
		if f.isTrailer() {
			trailerFrame = f
		} else {
			dataFrames = append(dataFrames, f.Payload)
		}
	}

	if len(dataFrames) != 3 {
		t.Fatalf("got %d data frames, want 3", len(dataFrames))
	}
	for i, want := range []string{"msg1", "msg2", "msg3"} {
		if string(dataFrames[i]) != want {
			t.Errorf("frame[%d] = %q, want %q", i, dataFrames[i], want)
		}
	}
	if trailerFrame == nil {
		t.Fatal("missing trailer frame")
	}
	trailers := parseTrailerFrame(trailerFrame.Payload)
	if trailers["grpc-status"] != "0" {
		t.Errorf("grpc-status = %q, want %q", trailers["grpc-status"], "0")
	}
}

func TestServerStreamTextMode(t *testing.T) {
	tr := New()
	cfg := config.ProtocolConfig{
		Type: "grpc_web",
		GRPCWeb: config.GRPCWebTranslateConfig{
			Timeout:  5 * time.Second,
			TextMode: true,
		},
	}

	// Stream 2 messages.
	grpcSrv, addr := startMockStreamingGRPCServer(t, [][]byte{
		[]byte("alpha"),
		[]byte("beta"),
	})
	defer grpcSrv.Stop()

	bal := &mockBalancer{
		backend: &loadbalancer.Backend{URL: addr},
	}

	handler, err := tr.Handler("text-stream-route", bal, cfg)
	if err != nil {
		t.Fatalf("Handler() error: %v", err)
	}

	rawBody := encodeDataFrame([]byte("request"))
	encodedBody := base64Encode(rawBody)
	req := httptest.NewRequest("POST", "/mock.StreamService/StreamMethod?streaming=server", bytes.NewReader(encodedBody))
	req.Header.Set("Content-Type", "application/grpc-web-text+proto")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/grpc-web-text+proto" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/grpc-web-text+proto")
	}

	// Each frame should be independently base64-encoded.
	// Verify by decoding each expected frame from the body.
	respBody := w.Body.Bytes()

	expected := [][]byte{
		base64Encode(encodeDataFrame([]byte("alpha"))),
		base64Encode(encodeDataFrame([]byte("beta"))),
	}
	for _, exp := range expected {
		if !bytes.Contains(respBody, exp) {
			t.Errorf("response body missing expected base64-encoded frame")
		}
	}

	// The trailer should also be present and base64-encoded.
	// Decode what's left after the two data frames.
	remaining := respBody
	for _, exp := range expected {
		idx := bytes.Index(remaining, exp)
		if idx == -1 {
			t.Fatalf("could not find expected frame in remaining body")
		}
		remaining = remaining[idx+len(exp):]
	}
	// remaining should be the base64-encoded trailer frame.
	if len(remaining) == 0 {
		t.Fatal("no trailer frame in response")
	}
	trailerBytes, err := base64Decode(remaining)
	if err != nil {
		t.Fatalf("failed to base64-decode trailer: %v", err)
	}
	tf, err := decodeGRPCWebFrame(bytes.NewReader(trailerBytes), 0)
	if err != nil {
		t.Fatalf("failed to decode trailer frame: %v", err)
	}
	if !tf.isTrailer() {
		t.Error("expected trailer frame")
	}
	trailers := parseTrailerFrame(tf.Payload)
	if trailers["grpc-status"] != "0" {
		t.Errorf("grpc-status = %q, want %q", trailers["grpc-status"], "0")
	}
}

func TestExtractMetadataSkipsStreamingHeader(t *testing.T) {
	req, _ := http.NewRequest("POST", "/test", nil)
	req.Header.Set("X-Grpc-Web-Streaming", "server")
	req.Header.Set("X-Custom", "keep")

	md := extractMetadata(req)
	if v := md.Get("x-grpc-web-streaming"); len(v) > 0 {
		t.Errorf("x-grpc-web-streaming should be skipped, got %v", v)
	}
	if v := md.Get("x-custom"); len(v) == 0 || v[0] != "keep" {
		t.Errorf("x-custom: got %v, want [keep]", v)
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

func (m *mockBalancer) GetBackendByURL(url string) *loadbalancer.Backend {
	if m.backend != nil && m.backend.URL == url {
		return m.backend
	}
	return nil
}

// startMockStreamingGRPCServer starts a gRPC server with a single streaming method
// that responds with the given messages. Returns the server and the address to dial.
func startMockStreamingGRPCServer(t *testing.T, messages [][]byte) (*grpc.Server, string) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	srv := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}))

	// Register a generic service handler that streams the messages.
	srv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "mock.StreamService",
		HandlerType: (*interface{})(nil),
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "StreamMethod",
				ServerStreams: true,
				Handler: func(srv interface{}, stream grpc.ServerStream) error {
					// Receive the client's single request.
					var req []byte
					if err := stream.RecvMsg(&req); err != nil {
						return err
					}
					// Send all messages.
					for _, msg := range messages {
						if err := stream.SendMsg(&msg); err != nil {
							return err
						}
					}
					return nil
				},
			},
		},
		Metadata: "",
	}, &struct{}{})

	go srv.Serve(lis)

	return srv, lis.Addr().String()
}
