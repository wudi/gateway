// Package bufutil provides a shared buffering http.ResponseWriter for
// middleware that needs to capture an entire response (status, headers, body)
// before post-processing and flushing to the real writer.
package bufutil

import (
	"bytes"
	"net/http"
	"strconv"
)

// Writer is a full-buffering http.ResponseWriter that captures status,
// headers, and body. Nothing is written to any underlying writer; the caller
// is responsible for flushing via FlushTo or reading fields directly.
type Writer struct {
	StatusCode int
	Body       bytes.Buffer
	header     http.Header
}

// New creates a Writer with status 200 and empty headers.
func New() *Writer {
	return &Writer{
		StatusCode: http.StatusOK,
		header:     make(http.Header),
	}
}

// Header returns the buffered response headers.
func (w *Writer) Header() http.Header { return w.header }

// WriteHeader captures the status code without writing it anywhere.
func (w *Writer) WriteHeader(code int) { w.StatusCode = code }

// Write appends b to the internal body buffer.
func (w *Writer) Write(b []byte) (int, error) { return w.Body.Write(b) }

// Flush is a no-op; everything stays buffered until FlushTo is called.
func (w *Writer) Flush() {}

// FlushTo writes the buffered response (headers, status, body) to dst.
func (w *Writer) FlushTo(dst http.ResponseWriter) {
	CopyHeaders(dst.Header(), w.header)
	dst.WriteHeader(w.StatusCode)
	if w.Body.Len() > 0 {
		dst.Write(w.Body.Bytes())
	}
}

// FlushToWithLength writes the buffered response to dst, setting
// Content-Length to match the (possibly transformed) body.
func (w *Writer) FlushToWithLength(dst http.ResponseWriter, body []byte) {
	CopyHeaders(dst.Header(), w.header)
	dst.Header().Set("Content-Length", strconv.Itoa(len(body)))
	dst.WriteHeader(w.StatusCode)
	dst.Write(body)
}

// CopyHeaders copies all header values from src into dst.
func CopyHeaders(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
