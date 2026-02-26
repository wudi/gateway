package connect

import (
	"io"
	"net"
	"net/http"
	"path"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
)

// TunnelHandler handles HTTP CONNECT tunneling for a single route.
type TunnelHandler struct {
	allowedHosts   []string
	allowedPorts   map[int]bool
	connectTimeout time.Duration
	idleTimeout    time.Duration
	maxTunnels     int
	activeTunnels  atomic.Int64
	totalTunnels   atomic.Int64
	totalBytes     atomic.Int64
}

// New creates a TunnelHandler from config.
func New(cfg config.ConnectConfig) *TunnelHandler {
	connectTimeout := cfg.ConnectTimeout
	if connectTimeout == 0 {
		connectTimeout = 10 * time.Second
	}
	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 300 * time.Second
	}
	maxTunnels := cfg.MaxTunnels
	if maxTunnels == 0 {
		maxTunnels = 100
	}

	ports := make(map[int]bool, len(cfg.AllowedPorts))
	if len(cfg.AllowedPorts) == 0 {
		// Default: only allow port 443
		ports[443] = true
	} else {
		for _, p := range cfg.AllowedPorts {
			ports[p] = true
		}
	}

	return &TunnelHandler{
		allowedHosts:   cfg.AllowedHosts,
		allowedPorts:   ports,
		connectTimeout: connectTimeout,
		idleTimeout:    idleTimeout,
		maxTunnels:     maxTunnels,
	}
}

// Middleware returns a middleware that intercepts HTTP CONNECT requests.
func (h *TunnelHandler) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodConnect {
				next.ServeHTTP(w, r)
				return
			}
			h.handleConnect(w, r)
		})
	}
}

func (h *TunnelHandler) handleConnect(w http.ResponseWriter, r *http.Request) {
	// Parse host:port from r.Host first, then r.URL.Host
	host, portStr, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, portStr, err = net.SplitHostPort(r.URL.Host)
		if err != nil {
			http.Error(w, "invalid CONNECT target", http.StatusBadRequest)
			return
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}

	// Validate host against allowedHosts (glob matching)
	if !h.isHostAllowed(host) {
		http.Error(w, "host not allowed", http.StatusForbidden)
		return
	}

	// Validate port
	if !h.allowedPorts[port] {
		http.Error(w, "port not allowed", http.StatusForbidden)
		return
	}

	// Check tunnel limit
	if h.maxTunnels > 0 && h.activeTunnels.Load() >= int64(h.maxTunnels) {
		http.Error(w, "too many tunnels", http.StatusServiceUnavailable)
		return
	}

	// Dial upstream
	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(host, portStr), h.connectTimeout)
	if err != nil {
		http.Error(w, "upstream connection failed", http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	// Hijack client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "hijack failed", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Send 200 Connection Established
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	h.activeTunnels.Add(1)
	h.totalTunnels.Add(1)
	defer h.activeTunnels.Add(-1)

	// Set idle timeout on both connections
	if h.idleTimeout > 0 {
		deadline := time.Now().Add(h.idleTimeout)
		_ = upstream.SetDeadline(deadline)
		if dc, ok := clientConn.(interface{ SetDeadline(time.Time) error }); ok {
			_ = dc.SetDeadline(deadline)
		}
	}

	// Bidirectional copy
	done := make(chan struct{}, 2)
	go func() {
		n, _ := io.Copy(upstream, clientConn)
		h.totalBytes.Add(n)
		if tc, ok := upstream.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		n, _ := io.Copy(clientConn, upstream)
		h.totalBytes.Add(n)
		if tc, ok := clientConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		done <- struct{}{}
	}()

	// Wait for one direction to complete, then drain the other
	<-done
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
}

// isHostAllowed checks whether the host is in the allowedHosts list using glob matching.
// An empty allowedHosts list means all hosts are allowed.
func (h *TunnelHandler) isHostAllowed(host string) bool {
	if len(h.allowedHosts) == 0 {
		return true
	}
	for _, pattern := range h.allowedHosts {
		if matched, _ := path.Match(pattern, host); matched {
			return true
		}
	}
	return false
}

// ActiveTunnels returns the current number of active tunnels.
func (h *TunnelHandler) ActiveTunnels() int64 {
	return h.activeTunnels.Load()
}

// TotalTunnels returns the total number of tunnels that have been opened.
func (h *TunnelHandler) TotalTunnels() int64 {
	return h.totalTunnels.Load()
}

// TotalBytes returns the total number of bytes relayed across all tunnels.
func (h *TunnelHandler) TotalBytes() int64 {
	return h.totalBytes.Load()
}

// ConnectByRoute manages per-route CONNECT tunnel handlers.
type ConnectByRoute struct {
	byroute.Manager[*TunnelHandler]
}

// NewConnectByRoute creates a new per-route CONNECT tunnel handler manager.
func NewConnectByRoute() *ConnectByRoute {
	return &ConnectByRoute{}
}

// AddRoute adds a CONNECT tunnel handler for a route.
func (c *ConnectByRoute) AddRoute(routeID string, cfg config.ConnectConfig) {
	c.Add(routeID, New(cfg))
}

// GetHandler returns the CONNECT tunnel handler for a route.
func (c *ConnectByRoute) GetHandler(routeID string) *TunnelHandler {
	v, _ := c.Get(routeID)
	return v
}

// Stats returns per-route CONNECT tunnel stats.
func (c *ConnectByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&c.Manager, func(h *TunnelHandler) interface{} {
		return map[string]interface{}{
			"active_tunnels": h.ActiveTunnels(),
			"total_tunnels":  h.TotalTunnels(),
			"total_bytes":    h.TotalBytes(),
		}
	})
}
