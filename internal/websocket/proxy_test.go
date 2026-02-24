package websocket

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestIsUpgradeRequest(t *testing.T) {
	tests := []struct {
		name       string
		connection string
		upgrade    string
		want       bool
	}{
		{"valid websocket", "Upgrade", "websocket", true},
		{"case insensitive", "upgrade", "WebSocket", true},
		{"keep-alive, upgrade", "keep-alive, Upgrade", "websocket", true},
		{"no connection header", "", "websocket", false},
		{"no upgrade header", "Upgrade", "", false},
		{"wrong upgrade", "Upgrade", "h2c", false},
		{"no headers", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			if tt.connection != "" {
				req.Header.Set("Connection", tt.connection)
			}
			if tt.upgrade != "" {
				req.Header.Set("Upgrade", tt.upgrade)
			}

			got := IsUpgradeRequest(req)
			if got != tt.want {
				t.Errorf("IsUpgradeRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewProxyDefaults(t *testing.T) {
	p := NewProxy(config.WebSocketConfig{})

	if p.readBufferSize != 4096 {
		t.Errorf("expected readBufferSize 4096, got %d", p.readBufferSize)
	}
	if p.writeBufferSize != 4096 {
		t.Errorf("expected writeBufferSize 4096, got %d", p.writeBufferSize)
	}
	if p.readTimeout != 60*time.Second {
		t.Errorf("expected readTimeout 60s, got %v", p.readTimeout)
	}
	if p.writeTimeout != 10*time.Second {
		t.Errorf("expected writeTimeout 10s, got %v", p.writeTimeout)
	}
	if p.pingInterval != 30*time.Second {
		t.Errorf("expected pingInterval 30s, got %v", p.pingInterval)
	}
	if p.pongTimeout != 60*time.Second {
		t.Errorf("expected pongTimeout 60s, got %v", p.pongTimeout)
	}
}

func TestNewProxyCustomConfig(t *testing.T) {
	p := NewProxy(config.WebSocketConfig{
		ReadBufferSize:  8192,
		WriteBufferSize: 8192,
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    5 * time.Second,
		PingInterval:    15 * time.Second,
		PongTimeout:     30 * time.Second,
	})

	if p.readBufferSize != 8192 {
		t.Errorf("expected readBufferSize 8192, got %d", p.readBufferSize)
	}
	if p.writeBufferSize != 8192 {
		t.Errorf("expected writeBufferSize 8192, got %d", p.writeBufferSize)
	}
	if p.readTimeout != 30*time.Second {
		t.Errorf("expected readTimeout 30s, got %v", p.readTimeout)
	}
}

func TestProxyServeHTTPNoHijack(t *testing.T) {
	p := NewProxy(config.WebSocketConfig{})

	// httptest.ResponseRecorder does not implement Hijacker
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")

	p.ServeHTTP(w, r, "http://localhost:9999")

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when hijack not supported, got %d", w.Code)
	}
}

func TestProxyServeHTTPInvalidBackend(t *testing.T) {
	p := NewProxy(config.WebSocketConfig{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")

	p.ServeHTTP(w, r, "://invalid")

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for invalid backend, got %d", w.Code)
	}
}

// mockHijackResponseWriter implements http.Hijacker for testing
type mockHijackResponseWriter struct {
	http.ResponseWriter
	conn net.Conn
}

func (m *mockHijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	reader := bufio.NewReader(m.conn)
	writer := bufio.NewWriter(m.conn)
	return m.conn, bufio.NewReadWriter(reader, writer), nil
}

func TestProxyEndToEnd(t *testing.T) {
	// Create a mock backend that responds with 101
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer backendListener.Close()

	go func() {
		conn, err := backendListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// Use a buffered reader to properly consume the HTTP request
		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}

		if req.Header.Get("Upgrade") != "websocket" {
			t.Errorf("expected Upgrade: websocket header")
			return
		}

		// Send 101 response
		resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"
		conn.Write([]byte(resp))

		// Echo data back
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			if err != nil {
				return
			}
			conn.Write(buf[:n])
		}
	}()

	// Create client-side pipe
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	p := NewProxy(config.WebSocketConfig{})

	hijackWriter := &mockHijackResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		conn:           serverConn,
	}

	r := httptest.NewRequest("GET", "/ws", nil)
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Sec-WebSocket-Version", "13")
	r.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	backendURL := "http://" + backendListener.Addr().String()

	done := make(chan struct{})
	go func() {
		p.ServeHTTP(hijackWriter, r, backendURL)
		close(done)
	}()

	// Read the 101 response from the proxy
	buf := make([]byte, 4096)
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := clientConn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read 101 response: %v", err)
	}

	respStr := string(buf[:n])
	if !strings.Contains(respStr, "101") {
		t.Errorf("expected 101 response, got: %s", respStr)
	}

	// Send data through the proxy
	clientConn.Write([]byte("hello"))

	// Read echo back
	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err = clientConn.Read(buf)
	if err != nil {
		t.Fatalf("failed to read echo: %v", err)
	}

	if string(buf[:n]) != "hello" {
		t.Errorf("expected 'hello', got '%s'", string(buf[:n]))
	}

	// Close connections to clean up
	clientConn.Close()
	serverConn.Close()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		// Proxy may still be cleaning up
	}
}
