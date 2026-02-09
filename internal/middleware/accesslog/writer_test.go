package accesslog

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBodyCapturingWriter_WriteThrough(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 1024)

	w.WriteHeader(http.StatusOK)
	n, err := w.Write([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Errorf("Write returned %d, want 11", n)
	}

	// Verify write-through
	if rec.Body.String() != "hello world" {
		t.Errorf("underlying writer got %q", rec.Body.String())
	}

	// Verify captured body
	if w.CapturedBody() != "hello world" {
		t.Errorf("captured body = %q", w.CapturedBody())
	}
	if w.StatusCode() != http.StatusOK {
		t.Errorf("status = %d, want %d", w.StatusCode(), http.StatusOK)
	}
	if w.IsTruncated() {
		t.Error("should not be truncated")
	}
}

func TestBodyCapturingWriter_MaxSizeTruncation(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 5)

	w.Write([]byte("hello world"))

	// Underlying writer should get all bytes
	if rec.Body.String() != "hello world" {
		t.Errorf("underlying writer got %q", rec.Body.String())
	}

	// Capture should be truncated
	if w.CapturedBody() != "hello" {
		t.Errorf("captured body = %q, want %q", w.CapturedBody(), "hello")
	}
	if !w.IsTruncated() {
		t.Error("should be truncated")
	}
}

func TestBodyCapturingWriter_MultipleWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 10)

	w.Write([]byte("abc"))
	w.Write([]byte("defgh"))
	w.Write([]byte("ijklm"))

	// All bytes pass through
	if rec.Body.String() != "abcdefghijklm" {
		t.Errorf("underlying writer got %q", rec.Body.String())
	}

	// Capture limited to 10
	if w.CapturedBody() != "abcdefghij" {
		t.Errorf("captured body = %q, want %q", w.CapturedBody(), "abcdefghij")
	}
	if !w.IsTruncated() {
		t.Error("should be truncated")
	}
}

func TestBodyCapturingWriter_DefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 1024)
	// No WriteHeader call
	if w.StatusCode() != http.StatusOK {
		t.Errorf("default status = %d, want %d", w.StatusCode(), http.StatusOK)
	}
}

func TestBodyCapturingWriter_StatusCapture(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 1024)
	w.WriteHeader(http.StatusNotFound)
	if w.StatusCode() != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.StatusCode(), http.StatusNotFound)
	}
}

func TestBodyCapturingWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 1024)
	w.Write([]byte("data"))
	// Should not panic
	w.Flush()
}

func TestBodyCapturingWriter_Hijack(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 1024)
	_, _, err := w.Hijack()
	if err != http.ErrNotSupported {
		t.Errorf("Hijack error = %v, want ErrNotSupported", err)
	}
}

func TestBodyCapturingWriter_ZeroMaxSize(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewBodyCapturingWriter(rec, 0)
	w.Write([]byte("data"))

	if w.CapturedBody() != "" {
		t.Errorf("captured body with maxSize=0 should be empty, got %q", w.CapturedBody())
	}
	if !w.IsTruncated() {
		t.Error("should be truncated with maxSize=0")
	}
	// Write-through still works
	if rec.Body.String() != "data" {
		t.Errorf("underlying writer got %q", rec.Body.String())
	}
}
