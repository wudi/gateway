package grpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestIsGRPCRequest(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"gRPC request", "application/grpc", true},
		{"gRPC-web request", "application/grpc-web", true},
		{"JSON request", "application/json", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", nil)
			if tt.contentType != "" {
				r.Header.Set("Content-Type", tt.contentType)
			}

			if got := IsGRPCRequest(r); got != tt.want {
				t.Errorf("IsGRPCRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPrepareRequest(t *testing.T) {
	h := New(config.GRPCConfig{Enabled: true})

	r := httptest.NewRequest("POST", "/", nil)
	r, cancel := h.PrepareRequest(r)
	defer cancel()

	if r.ProtoMajor != 2 {
		t.Errorf("expected HTTP/2, got HTTP/%d", r.ProtoMajor)
	}

	if r.Header.Get("TE") != "trailers" {
		t.Error("expected TE: trailers header")
	}
}

func TestPrepareRequestDisabled(t *testing.T) {
	h := New(config.GRPCConfig{Enabled: false})

	r := httptest.NewRequest("POST", "/", nil)
	r, cancel := h.PrepareRequest(r)
	defer cancel()

	if r.ProtoMajor != 1 {
		t.Errorf("disabled handler should not modify proto version")
	}
}

func TestPrepareRequestWithAuthority(t *testing.T) {
	h := New(config.GRPCConfig{
		Enabled:   true,
		Authority: "custom.authority",
	})

	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r, cancel := h.PrepareRequest(r)
	defer cancel()

	if r.Host != "custom.authority" {
		t.Errorf("expected host 'custom.authority', got %q", r.Host)
	}
}

func TestPrepareRequestWithDeadline(t *testing.T) {
	h := New(config.GRPCConfig{
		Enabled:             true,
		DeadlinePropagation: true,
	})

	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Set("grpc-timeout", "5S")

	r, cancel := h.PrepareRequest(r)
	defer cancel()

	if _, ok := r.Context().Deadline(); !ok {
		t.Error("expected deadline to be set with propagation enabled")
	}

	if h.deadlinesSet.Load() != 1 {
		t.Errorf("expected deadlinesSet=1, got %d", h.deadlinesSet.Load())
	}
}

func TestPrepareRequestWithMetadata(t *testing.T) {
	h := New(config.GRPCConfig{
		Enabled: true,
		MetadataTransforms: config.GRPCMetadataTransforms{
			RequestMap: map[string]string{
				"X-Custom": "x-grpc-custom",
			},
		},
	})

	r := httptest.NewRequest("POST", "/pkg.Svc/Method", nil)
	r.Header.Set("X-Custom", "value")

	r, cancel := h.PrepareRequest(r)
	defer cancel()

	if v := r.Header.Get("X-Grpc-Custom"); v != "value" {
		t.Errorf("expected metadata transform, got %q", v)
	}
}

func TestProcessResponse(t *testing.T) {
	h := New(config.GRPCConfig{
		Enabled: true,
		MetadataTransforms: config.GRPCMetadataTransforms{
			ResponseMap: map[string]string{
				"x-grpc-trace": "X-Trace-Id",
			},
		},
	})

	rec := httptest.NewRecorder()
	rec.Header().Set("X-Grpc-Trace", "trace123")

	h.ProcessResponse(rec)

	if v := rec.Header().Get("X-Trace-Id"); v != "trace123" {
		t.Errorf("expected response metadata transform, got %q", v)
	}
}

func TestHandlerStats(t *testing.T) {
	h := New(config.GRPCConfig{
		Enabled:             true,
		DeadlinePropagation: true,
		MaxRecvMsgSize:      4096,
		Authority:           "test.svc",
	})

	stats := h.Stats()
	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	if stats["deadline_propagation"] != true {
		t.Error("expected deadline_propagation=true")
	}
	if stats["max_recv_msg_size"] != 4096 {
		t.Error("expected max_recv_msg_size=4096")
	}
	if stats["authority"] != "test.svc" {
		t.Error("expected authority=test.svc")
	}
}

func TestMapStatusCode(t *testing.T) {
	tests := []struct {
		name       string
		grpcStatus string
		httpStatus int
		want       int
	}{
		{"OK", "0", 200, 200},
		{"UNAVAILABLE", "14", 200, 503},
		{"NOT_FOUND", "5", 200, 404},
		{"INTERNAL", "13", 200, 500},
		{"empty (OK)", "", 200, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.httpStatus,
				Header:     http.Header{},
				Trailer:    http.Header{},
			}
			if tt.grpcStatus != "" {
				resp.Header.Set("Grpc-Status", tt.grpcStatus)
			}

			if got := MapStatusCode(resp); got != tt.want {
				t.Errorf("MapStatusCode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIsRetryableGRPCStatus(t *testing.T) {
	if !IsRetryableGRPCStatus("14") {
		t.Error("UNAVAILABLE should be retryable")
	}

	if IsRetryableGRPCStatus("5") {
		t.Error("NOT_FOUND should not be retryable")
	}
}

func TestGRPCByRoute(t *testing.T) {
	m := NewGRPCByRoute()
	m.AddRoute("route1", config.GRPCConfig{Enabled: true})

	h := m.GetHandler("route1")
	if h == nil {
		t.Fatal("expected handler for route1")
	}
	if !h.IsEnabled() {
		t.Error("expected handler to be enabled")
	}

	if m.GetHandler("unknown") != nil {
		t.Error("expected nil for unknown route")
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}
