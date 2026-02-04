package grpc

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
	h := New(true)

	r := httptest.NewRequest("POST", "/", nil)
	h.PrepareRequest(r)

	if r.ProtoMajor != 2 {
		t.Errorf("expected HTTP/2, got HTTP/%d", r.ProtoMajor)
	}

	if r.Header.Get("TE") != "trailers" {
		t.Error("expected TE: trailers header")
	}
}

func TestPrepareRequestDisabled(t *testing.T) {
	h := New(false)

	r := httptest.NewRequest("POST", "/", nil)
	h.PrepareRequest(r)

	if r.ProtoMajor != 1 {
		t.Errorf("disabled handler should not modify proto version")
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
