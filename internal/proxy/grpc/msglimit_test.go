package grpc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitRequestBody(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", 100))
	r := httptest.NewRequest("POST", "/pkg.Svc/Method", body)

	LimitRequestBody(r, 50)

	data, err := io.ReadAll(r.Body)
	if err == nil {
		t.Fatal("expected error for body exceeding limit")
	}
	_ = data
	if !strings.Contains(err.Error(), "larger than max") {
		t.Errorf("expected size error, got: %v", err)
	}
}

func TestLimitRequestBodyWithinLimit(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", 50))
	r := httptest.NewRequest("POST", "/pkg.Svc/Method", body)

	LimitRequestBody(r, 100)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 50 {
		t.Errorf("expected 50 bytes, got %d", len(data))
	}
}

func TestLimitRequestBodyUnlimited(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", 1000))
	r := httptest.NewRequest("POST", "/pkg.Svc/Method", body)

	LimitRequestBody(r, 0)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 1000 {
		t.Errorf("expected 1000 bytes, got %d", len(data))
	}
}

func TestWrapResponseWriterLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	w := WrapResponseWriter(rec, 50)

	_, err := w.Write([]byte(strings.Repeat("x", 30)))
	if err != nil {
		t.Fatalf("first write should succeed: %v", err)
	}

	_, err = w.Write([]byte(strings.Repeat("x", 30)))
	if err == nil {
		t.Fatal("expected error for response exceeding limit")
	}
	if !strings.Contains(err.Error(), "larger than max") {
		t.Errorf("expected size error, got: %v", err)
	}
}

func TestWrapResponseWriterNoLimit(t *testing.T) {
	rec := httptest.NewRecorder()
	w := WrapResponseWriter(rec, 0)

	if w != rec {
		t.Error("expected original writer when limit is 0")
	}
}

func TestWrapResponseWriterFlusher(t *testing.T) {
	rec := httptest.NewRecorder()
	w := WrapResponseWriter(rec, 100)

	if lw, ok := w.(*limitedResponseWriter); ok {
		lw.Flush() // should not panic
	}

	if lw, ok := w.(*limitedResponseWriter); ok {
		inner := lw.Unwrap()
		if inner != rec {
			t.Error("Unwrap should return original writer")
		}
	}
}

func TestWrapResponseWriterGRPCStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	w := WrapResponseWriter(rec, 10)

	w.Write([]byte(strings.Repeat("x", 20)))

	// After exceeding, Grpc-Status should be set to 8 (RESOURCE_EXHAUSTED)
	if status := rec.Header().Get("Grpc-Status"); status != "8" {
		t.Errorf("expected Grpc-Status 8, got %q", status)
	}
}

func TestLimitedReaderNilBody(t *testing.T) {
	r := &http.Request{}
	LimitRequestBody(r, 100)
	if r.Body != nil {
		t.Error("expected nil body to remain nil")
	}
}
