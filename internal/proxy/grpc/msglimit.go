package grpc

import (
	"fmt"
	"io"
	"net/http"
)

// MsgLimitConfig holds message size limit settings.
type MsgLimitConfig struct {
	MaxRecvMsgSize int // max request body size in bytes (0=unlimited)
	MaxSendMsgSize int // max response body size in bytes (0=unlimited)
}

// limitedReader wraps an io.ReadCloser to enforce max message size.
type limitedReader struct {
	rc      io.ReadCloser
	max     int
	read    int
	limited bool
}

func newLimitedReader(rc io.ReadCloser, max int) io.ReadCloser {
	if max <= 0 {
		return rc
	}
	return &limitedReader{rc: rc, max: max}
}

func (lr *limitedReader) Read(p []byte) (int, error) {
	n, err := lr.rc.Read(p)
	lr.read += n
	if lr.read > lr.max {
		lr.limited = true
		return n, fmt.Errorf("grpc: received message larger than max (%d vs. %d bytes)", lr.read, lr.max)
	}
	return n, err
}

func (lr *limitedReader) Close() error {
	return lr.rc.Close()
}

// LimitRequestBody wraps the request body with a size limiter.
// Writes gRPC RESOURCE_EXHAUSTED status on violation.
func LimitRequestBody(r *http.Request, maxRecv int) {
	if maxRecv <= 0 || r.Body == nil {
		return
	}
	r.Body = newLimitedReader(r.Body, maxRecv)
}

// limitedResponseWriter wraps http.ResponseWriter to enforce max send size.
type limitedResponseWriter struct {
	http.ResponseWriter
	max     int
	written int
	errored bool
}

// WrapResponseWriter wraps the response writer with send size enforcement.
// Returns nil if no limit is set.
func WrapResponseWriter(w http.ResponseWriter, maxSend int) http.ResponseWriter {
	if maxSend <= 0 {
		return w
	}
	return &limitedResponseWriter{ResponseWriter: w, max: maxSend}
}

func (lw *limitedResponseWriter) Write(p []byte) (int, error) {
	if lw.errored {
		return 0, fmt.Errorf("grpc: send message size exceeded")
	}
	lw.written += len(p)
	if lw.written > lw.max {
		lw.errored = true
		// Write gRPC RESOURCE_EXHAUSTED error
		lw.Header().Set("Grpc-Status", "8")
		lw.Header().Set("Grpc-Message", fmt.Sprintf("message larger than max (%d vs. %d bytes)", lw.written, lw.max))
		return 0, fmt.Errorf("grpc: sent message larger than max (%d vs. %d bytes)", lw.written, lw.max)
	}
	return lw.ResponseWriter.Write(p)
}

func (lw *limitedResponseWriter) Flush() {
	if f, ok := lw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (lw *limitedResponseWriter) Unwrap() http.ResponseWriter {
	return lw.ResponseWriter
}
