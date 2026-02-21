package mirror

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiffCapturingWriter_BasicCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 1024)

	dw.Header().Set("X-Custom", "hello")
	dw.WriteHeader(201)
	dw.Write([]byte("response body"))

	if dw.StatusCode() != 201 {
		t.Errorf("expected status 201, got %d", dw.StatusCode())
	}
	if string(dw.CapturedBody()) != "response body" {
		t.Errorf("expected 'response body', got %q", string(dw.CapturedBody()))
	}
	if dw.CapturedHeaders().Get("X-Custom") != "hello" {
		t.Errorf("expected X-Custom=hello, got %q", dw.CapturedHeaders().Get("X-Custom"))
	}
	if dw.BodyTruncated() {
		t.Error("body should not be truncated")
	}

	// Verify pass-through
	if rec.Code != 201 {
		t.Errorf("recorder should have status 201, got %d", rec.Code)
	}
	if rec.Body.String() != "response body" {
		t.Errorf("recorder body mismatch: %q", rec.Body.String())
	}
}

func TestDiffCapturingWriter_BodyTruncation(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 10)

	data := []byte("this is a longer body than the limit")
	dw.Write(data)

	if !dw.BodyTruncated() {
		t.Error("body should be truncated")
	}
	if len(dw.CapturedBody()) != 10 {
		t.Errorf("captured body should be 10 bytes, got %d", len(dw.CapturedBody()))
	}
	if string(dw.CapturedBody()) != "this is a " {
		t.Errorf("captured body mismatch: %q", string(dw.CapturedBody()))
	}

	// Hash should cover ALL data, not just captured
	expected := sha256.Sum256(data)
	if dw.BodyHash() != expected {
		t.Error("hash should cover entire body, not just captured portion")
	}

	// Pass-through should have full data
	if rec.Body.String() != string(data) {
		t.Error("pass-through should have full body")
	}
}

func TestDiffCapturingWriter_MultipleWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 100)

	dw.Write([]byte("hello "))
	dw.Write([]byte("world"))

	if string(dw.CapturedBody()) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(dw.CapturedBody()))
	}

	h := sha256.New()
	h.Write([]byte("hello "))
	h.Write([]byte("world"))
	var expected [32]byte
	copy(expected[:], h.Sum(nil))

	if dw.BodyHash() != expected {
		t.Error("hash mismatch")
	}
}

func TestDiffCapturingWriter_DefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 100)

	dw.Write([]byte("data"))

	if dw.StatusCode() != 200 {
		t.Errorf("expected default status 200, got %d", dw.StatusCode())
	}
}

func TestDiffCapturingWriter_HeadersSnapshotOnWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 100)

	dw.Header().Set("X-Before", "yes")
	dw.Write([]byte("data"))

	// Headers captured on first write
	if dw.CapturedHeaders().Get("X-Before") != "yes" {
		t.Error("headers should be captured on first write")
	}
}

func TestDiffCapturingWriter_EmptyHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 100)

	// No write or WriteHeader called
	hdrs := dw.CapturedHeaders()
	if hdrs == nil {
		t.Error("CapturedHeaders should return non-nil empty header")
	}
}

func TestDiffCapturingWriter_Flusher(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 100)

	// httptest.ResponseRecorder implements Flusher
	dw.Flush()

	if !rec.Flushed {
		t.Error("Flush should pass through to underlying writer")
	}
}

func TestDiffCapturingWriter_TruncationBoundary(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 5)

	// Write exactly at the boundary
	dw.Write([]byte("12345"))
	if dw.BodyTruncated() {
		t.Error("exact boundary should not truncate")
	}
	if string(dw.CapturedBody()) != "12345" {
		t.Errorf("expected '12345', got %q", string(dw.CapturedBody()))
	}

	// Write one more byte
	dw.Write([]byte("6"))
	if !dw.BodyTruncated() {
		t.Error("should be truncated after exceeding limit")
	}
	// Body should still be 5 bytes
	if len(dw.CapturedBody()) != 5 {
		t.Errorf("expected 5 bytes captured, got %d", len(dw.CapturedBody()))
	}
}

func TestDiffCapturingWriter_NonFlusherUnderlying(t *testing.T) {
	// Use a ResponseWriter that doesn't implement Flusher
	w := &nonFlusherWriter{header: http.Header{}}
	dw := NewDiffCapturingWriter(w, 100)

	// Should not panic
	dw.Flush()
}

type nonFlusherWriter struct {
	header http.Header
}

func (w *nonFlusherWriter) Header() http.Header      { return w.header }
func (w *nonFlusherWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nonFlusherWriter) WriteHeader(int)            {}

func TestDiffCapturingWriter_LargeBody(t *testing.T) {
	rec := httptest.NewRecorder()
	dw := NewDiffCapturingWriter(rec, 100)

	// Write 1000 bytes
	data := strings.Repeat("x", 1000)
	dw.Write([]byte(data))

	if !dw.BodyTruncated() {
		t.Error("should be truncated")
	}
	if len(dw.CapturedBody()) != 100 {
		t.Errorf("expected 100 captured bytes, got %d", len(dw.CapturedBody()))
	}

	// Full data should pass through
	if rec.Body.Len() != 1000 {
		t.Errorf("pass-through should have 1000 bytes, got %d", rec.Body.Len())
	}
}
