package connect

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestNonConnectPassesThrough(t *testing.T) {
	h := New(config.ConnectConfig{Enabled: true})
	called := false
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://example.com/", nil)
	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called for non-CONNECT request")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestConnectAllowedHostPort(t *testing.T) {
	// Start a TCP echo server as the upstream
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)

	h := New(config.ConnectConfig{
		Enabled:        true,
		AllowedHosts:   []string{"127.0.0.1"},
		AllowedPorts:   []int{addr.Port},
		ConnectTimeout: 5 * time.Second,
		IdleTimeout:    5 * time.Second,
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called for CONNECT")
	}))

	// We need a real TCP connection for Hijack to work, so use an httptest.Server
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Establish a raw TCP connection to the server
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Send CONNECT request
	target := fmt.Sprintf("127.0.0.1:%d", addr.Port)
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)

	// Read the response
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200 Connection Established, got %d", resp.StatusCode)
	}

	// Send data through the tunnel
	testData := "hello tunnel"
	fmt.Fprint(conn, testData)

	// Set a read deadline so we don't hang
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, len(testData))
	_, err = io.ReadFull(br, buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != testData {
		t.Errorf("expected %q, got %q", testData, string(buf))
	}
}

func TestConnectDisallowedHost(t *testing.T) {
	h := New(config.ConnectConfig{
		Enabled:      true,
		AllowedHosts: []string{"allowed.example.com"},
		AllowedPorts: []int{443},
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called")
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT forbidden.example.com:443 HTTP/1.1\r\nHost: forbidden.example.com:443\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestConnectDisallowedPort(t *testing.T) {
	h := New(config.ConnectConfig{
		Enabled:      true,
		AllowedHosts: []string{"*"},
		AllowedPorts: []int{443},
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called")
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT example.com:8080 HTTP/1.1\r\nHost: example.com:8080\r\n\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}
}

func TestConnectTunnelLimit(t *testing.T) {
	// Start a TCP listener that holds connections open
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold connection open; just read until closed
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(io.Discard, c)
			}(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)

	h := New(config.ConnectConfig{
		Enabled:        true,
		AllowedHosts:   []string{"127.0.0.1"},
		AllowedPorts:   []int{addr.Port},
		MaxTunnels:     1,
		ConnectTimeout: 5 * time.Second,
		IdleTimeout:    30 * time.Second,
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called")
	}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	target := fmt.Sprintf("127.0.0.1:%d", addr.Port)

	// First connection should succeed
	conn1, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn1.Close()

	fmt.Fprintf(conn1, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br1 := bufio.NewReader(conn1)
	resp1, err := http.ReadResponse(br1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp1.StatusCode != 200 {
		t.Fatalf("expected first CONNECT to succeed with 200, got %d", resp1.StatusCode)
	}

	// Wait a moment for the tunnel to register as active
	time.Sleep(50 * time.Millisecond)

	// Second connection should be rejected (503)
	conn2, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()

	fmt.Fprintf(conn2, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br2 := bufio.NewReader(conn2)
	resp2, err := http.ReadResponse(br2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for second tunnel, got %d", resp2.StatusCode)
	}
}

func TestConnectStats(t *testing.T) {
	// Start a TCP echo server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	addr := ln.Addr().(*net.TCPAddr)

	h := New(config.ConnectConfig{
		Enabled:        true,
		AllowedHosts:   []string{"127.0.0.1"},
		AllowedPorts:   []int{addr.Port},
		ConnectTimeout: 5 * time.Second,
		IdleTimeout:    5 * time.Second,
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	target := fmt.Sprintf("127.0.0.1:%d", addr.Port)

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Close the connection so the tunnel completes
	conn.Close()

	// Wait for the tunnel goroutines to finish
	time.Sleep(100 * time.Millisecond)

	if h.TotalTunnels() != 1 {
		t.Errorf("expected 1 total tunnel, got %d", h.TotalTunnels())
	}
	// After connection close, active should be 0
	if h.ActiveTunnels() != 0 {
		t.Errorf("expected 0 active tunnels, got %d", h.ActiveTunnels())
	}
}

func TestConnectByRoute(t *testing.T) {
	m := NewConnectByRoute()
	m.AddRoute("route1", config.ConnectConfig{
		Enabled:      true,
		AllowedHosts: []string{"*.example.com"},
		AllowedPorts: []int{443, 8443},
	})

	if m.GetHandler("route1") == nil {
		t.Error("expected handler for route1")
	}
	if m.GetHandler("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestHostGlobMatching(t *testing.T) {
	tests := []struct {
		patterns []string
		host     string
		allowed  bool
	}{
		{nil, "anything.com", true},                          // empty = all allowed
		{[]string{"*.example.com"}, "sub.example.com", true}, // glob match
		{[]string{"*.example.com"}, "example.com", false},    // no match
		{[]string{"exact.com"}, "exact.com", true},           // exact match
		{[]string{"exact.com"}, "other.com", false},          // no match
		{[]string{"*"}, "anything.com", true},                // wildcard all
	}

	for _, tt := range tests {
		h := &TunnelHandler{allowedHosts: tt.patterns}
		got := h.isHostAllowed(tt.host)
		if got != tt.allowed {
			t.Errorf("patterns=%v host=%s: expected %v, got %v", tt.patterns, tt.host, tt.allowed, got)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	h := New(config.ConnectConfig{Enabled: true})

	// Check defaults
	if h.connectTimeout != 10*time.Second {
		t.Errorf("expected 10s connect timeout, got %v", h.connectTimeout)
	}
	if h.idleTimeout != 300*time.Second {
		t.Errorf("expected 300s idle timeout, got %v", h.idleTimeout)
	}
	if h.maxTunnels != 100 {
		t.Errorf("expected 100 max tunnels, got %d", h.maxTunnels)
	}
	// Default allowed ports should be [443]
	if !h.allowedPorts[443] {
		t.Error("expected port 443 to be allowed by default")
	}
	if h.allowedPorts[80] {
		t.Error("expected port 80 to not be allowed by default")
	}
}
