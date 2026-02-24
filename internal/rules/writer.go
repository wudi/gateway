package rules

import (
	"bytes"
	"fmt"
	"net/http"
)

// RulesResponseWriter intercepts WriteHeader and Write to buffer the entire
// response until response rules have been evaluated and headers modified.
// After Flush() is called, further Write calls pass through directly.
type RulesResponseWriter struct {
	underlying http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	flushed    bool
}

// NewRulesResponseWriter wraps an http.ResponseWriter for response-phase rule evaluation.
func NewRulesResponseWriter(w http.ResponseWriter) *RulesResponseWriter {
	return &RulesResponseWriter{
		underlying: w,
		statusCode: http.StatusOK,
	}
}

// Header returns the real header map so rules can modify it before flush.
func (rw *RulesResponseWriter) Header() http.Header {
	return rw.underlying.Header()
}

// WriteHeader captures the status code but does NOT forward it yet.
func (rw *RulesResponseWriter) WriteHeader(code int) {
	if !rw.flushed {
		rw.statusCode = code
	}
}

// Write buffers data until Flush is called. After flush, writes pass through.
func (rw *RulesResponseWriter) Write(b []byte) (int, error) {
	if rw.flushed {
		return rw.underlying.Write(b)
	}
	return rw.body.Write(b)
}

// Flush sends the (possibly modified) status and buffered body to the underlying writer.
// It is safe to call multiple times; only the first call takes effect.
// Content-Length is deleted before writing since body may have been modified.
func (rw *RulesResponseWriter) Flush() {
	if rw.flushed {
		return
	}
	rw.flushed = true
	rw.underlying.Header().Del("Content-Length")
	if rw.body.Len() > 0 {
		rw.underlying.Header().Set("Content-Length", fmt.Sprintf("%d", rw.body.Len()))
	}
	rw.underlying.WriteHeader(rw.statusCode)
	if rw.body.Len() > 0 {
		rw.underlying.Write(rw.body.Bytes())
	}
}

// StatusCode returns the captured status code.
func (rw *RulesResponseWriter) StatusCode() int {
	return rw.statusCode
}

// SetStatusCode updates the buffered status code (pre-flush only).
func (rw *RulesResponseWriter) SetStatusCode(code int) {
	if !rw.flushed {
		rw.statusCode = code
	}
}

// ReadBody returns the buffered body as a string.
func (rw *RulesResponseWriter) ReadBody() string {
	return rw.body.String()
}

// SetBody replaces the buffered body (pre-flush only).
func (rw *RulesResponseWriter) SetBody(s string) {
	if !rw.flushed {
		rw.body.Reset()
		rw.body.WriteString(s)
	}
}

// Flushed returns whether the status has been flushed to the underlying writer.
func (rw *RulesResponseWriter) Flushed() bool {
	return rw.flushed
}
