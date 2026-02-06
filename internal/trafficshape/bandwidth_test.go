package trafficshape

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBandwidthLimiter_WrapRequest(t *testing.T) {
	bw := NewBandwidthLimiter(1024*1024, 0, 0, 0) // 1MB/s request, unlimited response

	body := strings.NewReader("hello world")
	r := httptest.NewRequest("POST", "/", body)
	bw.WrapRequest(r)

	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("unexpected error reading body: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}

	snap := bw.Snapshot()
	if snap.TotalRequestBytes != int64(len("hello world")) {
		t.Errorf("expected %d request bytes, got %d", len("hello world"), snap.TotalRequestBytes)
	}
}

func TestBandwidthLimiter_WrapResponse(t *testing.T) {
	bw := NewBandwidthLimiter(0, 1024*1024, 0, 0) // unlimited request, 1MB/s response

	rec := httptest.NewRecorder()
	w := bw.WrapResponse(rec)

	w.WriteHeader(http.StatusOK)
	n, err := w.Write([]byte("response data"))
	if err != nil {
		t.Fatalf("unexpected error writing: %v", err)
	}
	if n != len("response data") {
		t.Errorf("expected %d bytes written, got %d", len("response data"), n)
	}

	snap := bw.Snapshot()
	if snap.TotalResponseBytes != int64(len("response data")) {
		t.Errorf("expected %d response bytes, got %d", len("response data"), snap.TotalResponseBytes)
	}
}

func TestBandwidthLimiter_UnlimitedPassthrough(t *testing.T) {
	bw := NewBandwidthLimiter(0, 0, 0, 0) // all unlimited

	r := httptest.NewRequest("POST", "/", strings.NewReader("data"))
	bw.WrapRequest(r) // should be a no-op

	rec := httptest.NewRecorder()
	w := bw.WrapResponse(rec)
	// With unlimited, the response writer should be the original recorder
	if _, ok := w.(*httptest.ResponseRecorder); !ok {
		t.Error("expected unlimited WrapResponse to return original writer")
	}
}

func TestBandwidthLimiter_Snapshot(t *testing.T) {
	bw := NewBandwidthLimiter(1024, 2048, 4096, 8192)

	snap := bw.Snapshot()
	if snap.RequestRateBPS != 1024 {
		t.Errorf("expected request rate 1024, got %d", snap.RequestRateBPS)
	}
	if snap.ResponseRateBPS != 2048 {
		t.Errorf("expected response rate 2048, got %d", snap.ResponseRateBPS)
	}
}
