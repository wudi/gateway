package accesslog

import (
	"bufio"
	"net"
	"net/http"
)

// BodyCapturingWriter wraps an http.ResponseWriter to tee response body writes
// to an internal buffer up to maxSize. Writes pass through immediately.
type BodyCapturingWriter struct {
	http.ResponseWriter
	buf        []byte
	maxSize    int
	captured   int
	truncated  bool
	statusCode int
	wroteHeader bool
}

// NewBodyCapturingWriter creates a BodyCapturingWriter that captures up to maxSize bytes.
func NewBodyCapturingWriter(w http.ResponseWriter, maxSize int) *BodyCapturingWriter {
	return &BodyCapturingWriter{
		ResponseWriter: w,
		maxSize:        maxSize,
		statusCode:     http.StatusOK,
	}
}

// WriteHeader captures the status code and forwards to the underlying writer.
func (w *BodyCapturingWriter) WriteHeader(code int) {
	if !w.wroteHeader {
		w.statusCode = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write captures bytes up to maxSize and passes all bytes through immediately.
func (w *BodyCapturingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	// Capture up to maxSize
	if w.captured < w.maxSize {
		remaining := w.maxSize - w.captured
		toCapture := len(b)
		if toCapture > remaining {
			toCapture = remaining
			w.truncated = true
		}
		w.buf = append(w.buf, b[:toCapture]...)
		w.captured += toCapture
	} else if len(b) > 0 {
		w.truncated = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush implements http.Flusher.
func (w *BodyCapturingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack implements http.Hijacker.
func (w *BodyCapturingWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// StatusCode returns the captured HTTP status code.
func (w *BodyCapturingWriter) StatusCode() int {
	return w.statusCode
}

// CapturedBody returns the captured response body as a string.
func (w *BodyCapturingWriter) CapturedBody() string {
	return string(w.buf)
}

// IsTruncated returns true if the response body exceeded maxSize.
func (w *BodyCapturingWriter) IsTruncated() bool {
	return w.truncated
}
