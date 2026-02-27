package trafficshape

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"

	"golang.org/x/time/rate"
	"github.com/wudi/runway/internal/middleware"
)

// BandwidthLimiter limits request and response throughput using token bucket rate limiters.
type BandwidthLimiter struct {
	reqLimiter  *rate.Limiter // nil if request rate is unlimited
	respLimiter *rate.Limiter // nil if response rate is unlimited

	totalRequestBytes  atomic.Int64
	totalResponseBytes atomic.Int64
	requestRateBPS     int64
	responseRateBPS    int64
}

// NewBandwidthLimiter creates a new BandwidthLimiter.
// A rate of 0 means unlimited for that direction.
func NewBandwidthLimiter(requestRate, responseRate, requestBurst, responseBurst int64) *BandwidthLimiter {
	bw := &BandwidthLimiter{
		requestRateBPS:  requestRate,
		responseRateBPS: responseRate,
	}
	if requestRate > 0 {
		if requestBurst <= 0 {
			requestBurst = requestRate
		}
		bw.reqLimiter = rate.NewLimiter(rate.Limit(requestRate), int(requestBurst))
	}
	if responseRate > 0 {
		if responseBurst <= 0 {
			responseBurst = responseRate
		}
		bw.respLimiter = rate.NewLimiter(rate.Limit(responseRate), int(responseBurst))
	}
	return bw
}

// WrapRequest wraps the request body with bandwidth limiting. Returns the original body if unlimited.
func (bw *BandwidthLimiter) WrapRequest(r *http.Request) {
	if bw.reqLimiter == nil || r.Body == nil {
		return
	}
	r.Body = &rateLimitedReader{
		rc:      r.Body,
		limiter: bw.reqLimiter,
		ctx:     r.Context(),
		counter: &bw.totalRequestBytes,
	}
}

// WrapResponse wraps the response writer with bandwidth limiting. Returns the original writer if unlimited.
func (bw *BandwidthLimiter) WrapResponse(w http.ResponseWriter) http.ResponseWriter {
	if bw.respLimiter == nil {
		return w
	}
	return &rateLimitedWriter{
		ResponseWriter: w,
		limiter:        bw.respLimiter,
		counter:        &bw.totalResponseBytes,
	}
}

// Snapshot returns a point-in-time metrics snapshot.
func (bw *BandwidthLimiter) Snapshot() BandwidthSnapshot {
	return BandwidthSnapshot{
		RequestRateBPS:     bw.requestRateBPS,
		ResponseRateBPS:    bw.responseRateBPS,
		TotalRequestBytes:  bw.totalRequestBytes.Load(),
		TotalResponseBytes: bw.totalResponseBytes.Load(),
	}
}

// rateLimitedReader wraps an io.ReadCloser and applies bandwidth limiting.
type rateLimitedReader struct {
	rc      io.ReadCloser
	limiter *rate.Limiter
	ctx     context.Context
	counter *atomic.Int64
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	// Limit read size to burst so WaitN never exceeds burst
	burst := r.limiter.Burst()
	if len(p) > burst {
		p = p[:burst]
	}
	n, err := r.rc.Read(p)
	if n > 0 {
		r.counter.Add(int64(n))
		if waitErr := r.limiter.WaitN(r.ctx, n); waitErr != nil {
			return n, waitErr
		}
	}
	return n, err
}

func (r *rateLimitedReader) Close() error {
	return r.rc.Close()
}

// rateLimitedWriter wraps an http.ResponseWriter and applies bandwidth limiting.
type rateLimitedWriter struct {
	http.ResponseWriter
	limiter *rate.Limiter
	counter *atomic.Int64
}

func (w *rateLimitedWriter) Write(p []byte) (int, error) {
	ctx := context.Background()
	burst := w.limiter.Burst()
	written := 0
	for written < len(p) {
		chunk := len(p) - written
		if chunk > burst {
			chunk = burst
		}
		if err := w.limiter.WaitN(ctx, chunk); err != nil {
			return written, err
		}
		n, err := w.ResponseWriter.Write(p[written : written+chunk])
		written += n
		w.counter.Add(int64(n))
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

func (w *rateLimitedWriter) WriteHeader(code int) {
	w.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer if it implements http.Flusher.
func (w *rateLimitedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for interface detection.
func (w *rateLimitedWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Middleware returns a middleware that wraps request body and response writer with bandwidth limits.
func (bw *BandwidthLimiter) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw.WrapRequest(r)
			wrappedW := bw.WrapResponse(w)
			next.ServeHTTP(wrappedW, r)
		})
	}
}
