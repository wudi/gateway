package streaming

import (
	"bufio"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// StreamHandler controls response streaming behavior for a route.
type StreamHandler struct {
	flushInterval    time.Duration
	disableBuffering bool
	totalRequests    atomic.Int64
	flushedWrites    atomic.Int64
}

// New creates a StreamHandler from config.
func New(cfg config.StreamingConfig) *StreamHandler {
	return &StreamHandler{
		flushInterval:    cfg.FlushInterval,
		disableBuffering: cfg.DisableBuffering,
	}
}

// Middleware returns a middleware that wraps the ResponseWriter to control flushing.
func (s *StreamHandler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.totalRequests.Add(1)

			fw := &flushWriter{
				ResponseWriter: w,
				handler:        s,
			}

			if s.disableBuffering {
				// Every Write() will flush immediately; no ticker needed.
				next.ServeHTTP(fw, r)
				return
			}

			if s.flushInterval > 0 {
				fw.ticker = time.NewTicker(s.flushInterval)
				done := r.Context().Done()
				go func() {
					for {
						select {
						case <-fw.ticker.C:
							if f, ok := fw.ResponseWriter.(http.Flusher); ok {
								f.Flush()
							}
						case <-done:
							return
						}
					}
				}()
				next.ServeHTTP(fw, r)
				fw.ticker.Stop()
				return
			}

			// No streaming behaviour configured; pass through.
			next.ServeHTTP(w, r)
		})
	}
}

// TotalRequests returns the number of requests handled.
func (s *StreamHandler) TotalRequests() int64 {
	return s.totalRequests.Load()
}

// FlushedWrites returns the number of writes that triggered a flush.
func (s *StreamHandler) FlushedWrites() int64 {
	return s.flushedWrites.Load()
}

// flushWriter wraps an http.ResponseWriter to flush after every Write
// when disable_buffering is set, or provides the target for a periodic ticker.
type flushWriter struct {
	http.ResponseWriter
	handler *StreamHandler
	ticker  *time.Ticker // nil when disable_buffering
}

func (fw *flushWriter) Write(b []byte) (int, error) {
	n, err := fw.ResponseWriter.Write(b)
	if n > 0 && fw.handler.disableBuffering {
		if f, ok := fw.ResponseWriter.(http.Flusher); ok {
			f.Flush()
			fw.handler.flushedWrites.Add(1)
		}
	}
	return n, err
}

func (fw *flushWriter) Flush() {
	if f, ok := fw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (fw *flushWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := fw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// StreamByRoute manages per-route stream handlers.
type StreamByRoute = byroute.Factory[*StreamHandler, config.StreamingConfig]

// NewStreamByRoute creates a new per-route stream handler manager.
func NewStreamByRoute() *StreamByRoute {
	return byroute.SimpleFactory(New, func(h *StreamHandler) any {
		return map[string]interface{}{
			"total_requests": h.TotalRequests(),
			"flushed_writes": h.FlushedWrites(),
		}
	})
}
