package websocket

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/example/gateway/internal/config"
)

// Proxy handles WebSocket proxying via HTTP hijack
type Proxy struct {
	readBufferSize  int
	writeBufferSize int
	readTimeout     time.Duration
	writeTimeout    time.Duration
	pingInterval    time.Duration
	pongTimeout     time.Duration
}

// NewProxy creates a new WebSocket proxy
func NewProxy(cfg config.WebSocketConfig) *Proxy {
	readBuf := cfg.ReadBufferSize
	if readBuf <= 0 {
		readBuf = 4096
	}

	writeBuf := cfg.WriteBufferSize
	if writeBuf <= 0 {
		writeBuf = 4096
	}

	readTimeout := cfg.ReadTimeout
	if readTimeout <= 0 {
		readTimeout = 60 * time.Second
	}

	writeTimeout := cfg.WriteTimeout
	if writeTimeout <= 0 {
		writeTimeout = 10 * time.Second
	}

	pingInterval := cfg.PingInterval
	if pingInterval <= 0 {
		pingInterval = 30 * time.Second
	}

	pongTimeout := cfg.PongTimeout
	if pongTimeout <= 0 {
		pongTimeout = 60 * time.Second
	}

	return &Proxy{
		readBufferSize:  readBuf,
		writeBufferSize: writeBuf,
		readTimeout:     readTimeout,
		writeTimeout:    writeTimeout,
		pingInterval:    pingInterval,
		pongTimeout:     pongTimeout,
	}
}

// IsUpgradeRequest checks if the request is a WebSocket upgrade request
func IsUpgradeRequest(r *http.Request) bool {
	connection := strings.ToLower(r.Header.Get("Connection"))
	upgrade := strings.ToLower(r.Header.Get("Upgrade"))

	return strings.Contains(connection, "upgrade") && upgrade == "websocket"
}

// ServeHTTP proxies a WebSocket connection to the backend
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request, backendURL string) {
	// Parse backend URL
	target, err := url.Parse(backendURL)
	if err != nil {
		http.Error(w, "Bad Gateway: invalid backend URL", http.StatusBadGateway)
		return
	}

	// Hijack the client connection
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket upgrade not supported", http.StatusInternalServerError)
		return
	}

	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "Failed to hijack connection", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Determine backend address
	backendAddr := target.Host
	if !strings.Contains(backendAddr, ":") {
		if target.Scheme == "https" || target.Scheme == "wss" {
			backendAddr += ":443"
		} else {
			backendAddr += ":80"
		}
	}

	// Dial the backend
	backendConn, err := net.DialTimeout("tcp", backendAddr, 10*time.Second)
	if err != nil {
		log.Printf("WebSocket proxy: failed to dial backend %s: %v", backendAddr, err)
		clientBuf.WriteString("HTTP/1.1 502 Bad Gateway\r\n\r\n")
		clientBuf.Flush()
		return
	}
	defer backendConn.Close()

	// Forward the original upgrade request to the backend
	reqPath := r.URL.Path
	if r.URL.RawQuery != "" {
		reqPath += "?" + r.URL.RawQuery
	}

	// Write the request line
	backendConn.Write([]byte(r.Method + " " + reqPath + " HTTP/1.1\r\n"))

	// Write headers, updating Host
	r.Header.Set("Host", target.Host)
	for key, values := range r.Header {
		for _, v := range values {
			backendConn.Write([]byte(key + ": " + v + "\r\n"))
		}
	}
	backendConn.Write([]byte("\r\n"))

	// Read the backend response (101 Switching Protocols)
	buf := make([]byte, p.readBufferSize)
	n, err := backendConn.Read(buf)
	if err != nil {
		log.Printf("WebSocket proxy: failed to read backend response: %v", err)
		clientBuf.WriteString("HTTP/1.1 502 Bad Gateway\r\n\r\n")
		clientBuf.Flush()
		return
	}

	// Forward the backend's response to the client
	clientConn.Write(buf[:n])

	// Bidirectional copy
	errCh := make(chan error, 2)

	go func() {
		_, err := io.Copy(backendConn, clientConn)
		errCh <- err
	}()

	go func() {
		_, err := io.Copy(clientConn, backendConn)
		errCh <- err
	}()

	// Wait for either direction to finish
	<-errCh

	// Set a deadline to let the other direction finish
	clientConn.SetDeadline(time.Now().Add(1 * time.Second))
	backendConn.SetDeadline(time.Now().Add(1 * time.Second))
}
