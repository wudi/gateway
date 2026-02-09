package proxy

import (
	"context"
	"io"
	"time"
)

// idleTimeoutReader wraps an io.ReadCloser to enforce an idle timeout.
// If no data is read for the configured duration, Read returns context.DeadlineExceeded.
type idleTimeoutReader struct {
	rc      io.ReadCloser
	timeout time.Duration
}

// newIdleTimeoutReader wraps rc with idle timeout enforcement.
func newIdleTimeoutReader(rc io.ReadCloser, timeout time.Duration) *idleTimeoutReader {
	return &idleTimeoutReader{rc: rc, timeout: timeout}
}

// Read reads from the underlying reader with an idle timeout.
// If the read does not complete within the timeout, context.DeadlineExceeded is returned.
func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := r.rc.Read(p)
		ch <- result{n, err}
	}()

	select {
	case res := <-ch:
		return res.n, res.err
	case <-time.After(r.timeout):
		return 0, context.DeadlineExceeded
	}
}

// Close closes the underlying reader.
func (r *idleTimeoutReader) Close() error {
	return r.rc.Close()
}
