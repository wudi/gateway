package mirror

import (
	"bytes"
	"crypto/sha256"
	"hash"
	"net/http"
)

// DiffCapturingWriter wraps an http.ResponseWriter to capture the status code,
// response headers, and body (up to a size limit) for detailed diff comparison.
// All writes pass through to the underlying writer immediately.
type DiffCapturingWriter struct {
	http.ResponseWriter
	statusCode     int
	headers        http.Header
	body           bytes.Buffer
	hasher         hash.Hash
	maxCapture     int64
	captured       int64
	bodyTruncated  bool
	headerWritten  bool
}

// NewDiffCapturingWriter creates a DiffCapturingWriter wrapping w with the given body capture limit.
func NewDiffCapturingWriter(w http.ResponseWriter, maxCapture int64) *DiffCapturingWriter {
	return &DiffCapturingWriter{
		ResponseWriter: w,
		statusCode:     200,
		hasher:         sha256.New(),
		maxCapture:     maxCapture,
	}
}

// WriteHeader captures the status code and a snapshot of the response headers, then passes through.
func (dw *DiffCapturingWriter) WriteHeader(code int) {
	if !dw.headerWritten {
		dw.statusCode = code
		dw.headers = dw.ResponseWriter.Header().Clone()
		dw.headerWritten = true
	}
	dw.ResponseWriter.WriteHeader(code)
}

// Write passes data through to the underlying writer and tees it to the body buffer and hasher.
func (dw *DiffCapturingWriter) Write(b []byte) (int, error) {
	if !dw.headerWritten {
		dw.headers = dw.ResponseWriter.Header().Clone()
		dw.headerWritten = true
	}

	dw.hasher.Write(b)

	if !dw.bodyTruncated {
		remaining := dw.maxCapture - dw.captured
		if remaining > 0 {
			toWrite := b
			if int64(len(b)) > remaining {
				toWrite = b[:remaining]
				dw.bodyTruncated = true
			}
			dw.body.Write(toWrite)
			dw.captured += int64(len(toWrite))
		} else {
			dw.bodyTruncated = true
		}
	}

	return dw.ResponseWriter.Write(b)
}

// StatusCode returns the captured status code.
func (dw *DiffCapturingWriter) StatusCode() int {
	return dw.statusCode
}

// CapturedHeaders returns the snapshot of response headers taken at WriteHeader time.
func (dw *DiffCapturingWriter) CapturedHeaders() http.Header {
	if dw.headers == nil {
		return http.Header{}
	}
	return dw.headers
}

// CapturedBody returns the captured body bytes (up to the max capture limit).
func (dw *DiffCapturingWriter) CapturedBody() []byte {
	return dw.body.Bytes()
}

// BodyHash returns the SHA-256 hash of all bytes written.
func (dw *DiffCapturingWriter) BodyHash() [32]byte {
	var h [32]byte
	copy(h[:], dw.hasher.Sum(nil))
	return h
}

// BodyTruncated returns whether the body exceeded the capture limit.
func (dw *DiffCapturingWriter) BodyTruncated() bool {
	return dw.bodyTruncated
}

// Flush implements http.Flusher.
func (dw *DiffCapturingWriter) Flush() {
	if f, ok := dw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
