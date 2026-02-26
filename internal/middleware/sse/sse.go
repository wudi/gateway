package sse

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// SSEHandler manages SSE proxying for a single route.
type SSEHandler struct {
	heartbeatInterval  time.Duration
	retryMS            int
	connectEvent       string
	disconnectEvent    string
	maxIdle            time.Duration
	forwardLastEventID bool

	activeConns    atomic.Int64
	totalConns     atomic.Int64
	totalEvents    atomic.Int64
	heartbeatsSent atomic.Int64

	hub *Hub // non-nil when fan-out is enabled
}

// New creates an SSEHandler from config.
// ForwardLastEventID defaults to true (plain bool can't distinguish unset from false).
func New(cfg config.SSEConfig) *SSEHandler {
	return &SSEHandler{
		heartbeatInterval:  cfg.HeartbeatInterval,
		retryMS:            cfg.RetryMS,
		connectEvent:       cfg.ConnectEvent,
		disconnectEvent:    cfg.DisconnectEvent,
		maxIdle:            cfg.MaxIdle,
		forwardLastEventID: true,
	}
}

// Middleware returns an http middleware that intercepts SSE responses.
func (h *SSEHandler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Fan-out mode: serve from hub, never call next
			if h.hub != nil {
				h.activeConns.Add(1)
				h.totalConns.Add(1)
				defer h.activeConns.Add(-1)
				h.hub.ServeClient(w, r)
				return
			}

			// Forward Last-Event-ID to backend
			if h.forwardLastEventID {
				if lastID := r.Header.Get("Last-Event-ID"); lastID != "" {
					r.Header.Set("Last-Event-ID", lastID)
				}
			}

			sw := &sseResponseWriter{
				ResponseWriter: w,
				handler:        h,
				request:        r,
			}
			next.ServeHTTP(sw, r)
			sw.close()
		})
	}
}

// SetHub sets the fan-out hub for this handler.
func (h *SSEHandler) SetHub(hub *Hub) {
	h.hub = hub
}

// StopHub stops the fan-out hub if one is set.
func (h *SSEHandler) StopHub() {
	if h.hub != nil {
		h.hub.Stop()
	}
}

// Stats returns handler statistics.
func (h *SSEHandler) Stats() map[string]interface{} {
	stats := map[string]interface{}{
		"active_connections": h.activeConns.Load(),
		"total_connections":  h.totalConns.Load(),
		"total_events":       h.totalEvents.Load(),
		"heartbeats_sent":    h.heartbeatsSent.Load(),
	}
	if h.hub != nil {
		stats["fanout"] = h.hub.Stats()
	}
	return stats
}

// sseResponseWriter wraps http.ResponseWriter to handle SSE event-aware flushing.
type sseResponseWriter struct {
	http.ResponseWriter
	handler *SSEHandler
	request *http.Request

	sseMode     bool
	headersSent bool
	buf         []byte
	flusher     http.Flusher

	// heartbeat management
	heartbeatStop chan struct{}
	heartbeatOnce sync.Once
	idleMu        sync.Mutex
	lastActivity  time.Time

	// max idle timer
	maxIdleTimer *time.Timer

	closed bool
}

var (
	eventBoundary = []byte("\n\n")
	heartbeatMsg  = []byte(": heartbeat\n\n")
)

func (sw *sseResponseWriter) WriteHeader(statusCode int) {
	if sw.headersSent {
		return
	}
	sw.headersSent = true

	ct := sw.Header().Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		sw.sseMode = true
		sw.flusher, _ = sw.ResponseWriter.(http.Flusher)

		// Prevent downstream buffering
		sw.Header().Set("Cache-Control", "no-store")
		sw.Header().Del("Content-Length")

		sw.ResponseWriter.WriteHeader(statusCode)

		// Track connection
		sw.handler.activeConns.Add(1)
		sw.handler.totalConns.Add(1)

		// Inject retry field
		if sw.handler.retryMS > 0 {
			retryLine := fmt.Sprintf("retry: %d\n\n", sw.handler.retryMS)
			sw.ResponseWriter.Write([]byte(retryLine))
			if sw.flusher != nil {
				sw.flusher.Flush()
			}
		}

		// Inject connect event
		if sw.handler.connectEvent != "" {
			connectData := "data: " + sw.handler.connectEvent + "\n\n"
			sw.ResponseWriter.Write([]byte(connectData))
			if sw.flusher != nil {
				sw.flusher.Flush()
			}
		}

		sw.lastActivity = time.Now()

		// Start heartbeat goroutine
		if sw.handler.heartbeatInterval > 0 {
			sw.heartbeatStop = make(chan struct{})
			go sw.heartbeatLoop()
		}

		// Start max idle timer
		if sw.handler.maxIdle > 0 {
			sw.maxIdleTimer = time.AfterFunc(sw.handler.maxIdle, func() {
				// Cancel the request context to terminate the connection
				// We can't cancel the context directly, but we can close the
				// connection via hijack or just let the write fail.
				sw.close()
			})
		}

		return
	}

	sw.ResponseWriter.WriteHeader(statusCode)
}

func (sw *sseResponseWriter) Write(p []byte) (int, error) {
	if !sw.headersSent {
		sw.WriteHeader(http.StatusOK)
	}
	if !sw.sseMode {
		return sw.ResponseWriter.Write(p)
	}

	// SSE mode: buffer and flush on event boundaries
	sw.buf = append(sw.buf, p...)
	written := len(p)

	for {
		idx := bytes.Index(sw.buf, eventBoundary)
		if idx < 0 {
			break
		}

		// Write complete event including the boundary
		event := sw.buf[:idx+len(eventBoundary)]
		if _, err := sw.ResponseWriter.Write(event); err != nil {
			return written, err
		}
		if sw.flusher != nil {
			sw.flusher.Flush()
		}

		sw.handler.totalEvents.Add(1)
		sw.buf = sw.buf[idx+len(eventBoundary):]

		// Reset idle tracking
		sw.idleMu.Lock()
		sw.lastActivity = time.Now()
		sw.idleMu.Unlock()

		// Reset max idle timer
		if sw.maxIdleTimer != nil {
			sw.maxIdleTimer.Reset(sw.handler.maxIdle)
		}
	}

	return written, nil
}

func (sw *sseResponseWriter) Flush() {
	if f, ok := sw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for interface checks.
func (sw *sseResponseWriter) Unwrap() http.ResponseWriter {
	return sw.ResponseWriter
}

func (sw *sseResponseWriter) heartbeatLoop() {
	ticker := time.NewTicker(sw.handler.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-sw.heartbeatStop:
			return
		case <-ticker.C:
			sw.idleMu.Lock()
			idle := time.Since(sw.lastActivity)
			sw.idleMu.Unlock()

			if idle >= sw.handler.heartbeatInterval {
				if _, err := sw.ResponseWriter.Write(heartbeatMsg); err != nil {
					return
				}
				if sw.flusher != nil {
					sw.flusher.Flush()
				}
				sw.handler.heartbeatsSent.Add(1)

				sw.idleMu.Lock()
				sw.lastActivity = time.Now()
				sw.idleMu.Unlock()
			}
		}
	}
}

func (sw *sseResponseWriter) close() {
	if sw.closed {
		return
	}
	sw.closed = true

	if !sw.sseMode {
		return
	}

	// Inject disconnect event
	if sw.handler.disconnectEvent != "" {
		disconnectData := "data: " + sw.handler.disconnectEvent + "\n\n"
		sw.ResponseWriter.Write([]byte(disconnectData))
		if sw.flusher != nil {
			sw.flusher.Flush()
		}
	}

	// Stop heartbeat
	sw.heartbeatOnce.Do(func() {
		if sw.heartbeatStop != nil {
			close(sw.heartbeatStop)
		}
	})

	// Stop max idle timer
	if sw.maxIdleTimer != nil {
		sw.maxIdleTimer.Stop()
	}

	sw.handler.activeConns.Add(-1)
}

// SSEByRoute manages per-route SSE handlers.
type SSEByRoute struct {
	byroute.Manager[*SSEHandler]
}

// NewSSEByRoute creates a new per-route SSE handler manager.
func NewSSEByRoute() *SSEByRoute {
	return &SSEByRoute{}
}

// AddRoute adds an SSE handler for a route.
func (m *SSEByRoute) AddRoute(routeID string, cfg config.SSEConfig) {
	m.Add(routeID, New(cfg))
}

// GetHandler returns the SSE handler for a route.
func (m *SSEByRoute) GetHandler(routeID string) *SSEHandler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route SSE stats.
func (m *SSEByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *SSEHandler) interface{} {
		return h.Stats()
	})
}

// StopAllHubs stops all fan-out hubs across all routes.
func (m *SSEByRoute) StopAllHubs() {
	m.Range(func(id string, h *SSEHandler) bool {
		h.StopHub()
		return true
	})
}
