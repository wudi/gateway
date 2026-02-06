package mirror

import (
	"crypto/sha256"
	"hash"
	"net/http"
)

// CapturingWriter wraps an http.ResponseWriter to capture the status code
// and compute a streaming SHA-256 hash of the response body.
// Writes are passed through immediately (no buffering).
type CapturingWriter struct {
	http.ResponseWriter
	statusCode    int
	hasher        hash.Hash
	headerWritten bool
}

// NewCapturingWriter creates a CapturingWriter wrapping w.
func NewCapturingWriter(w http.ResponseWriter) *CapturingWriter {
	return &CapturingWriter{
		ResponseWriter: w,
		statusCode:     200,
		hasher:         sha256.New(),
	}
}

// WriteHeader captures the status code and passes through.
func (cw *CapturingWriter) WriteHeader(code int) {
	if !cw.headerWritten {
		cw.statusCode = code
		cw.headerWritten = true
	}
	cw.ResponseWriter.WriteHeader(code)
}

// Write passes data through and feeds it to the hasher.
func (cw *CapturingWriter) Write(b []byte) (int, error) {
	cw.hasher.Write(b)
	return cw.ResponseWriter.Write(b)
}

// StatusCode returns the captured status code.
func (cw *CapturingWriter) StatusCode() int {
	return cw.statusCode
}

// BodyHash returns the SHA-256 hash of all bytes written.
func (cw *CapturingWriter) BodyHash() [32]byte {
	var h [32]byte
	copy(h[:], cw.hasher.Sum(nil))
	return h
}

// Flush implements http.Flusher.
func (cw *CapturingWriter) Flush() {
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
