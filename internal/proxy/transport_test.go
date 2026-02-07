package proxy

import (
	"net"
	"testing"
	"time"
)

func TestNewTransportDefault(t *testing.T) {
	tr := NewTransport(DefaultTransportConfig)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.MaxIdleConns != 100 {
		t.Errorf("expected MaxIdleConns 100, got %d", tr.MaxIdleConns)
	}
}

func TestNewTransportWithResolver(t *testing.T) {
	resolver := &net.Resolver{PreferGo: true}
	cfg := DefaultTransportConfig
	cfg.Resolver = resolver

	tr := NewTransport(cfg)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	// The resolver is embedded inside the dialer closure; we verify
	// the transport was created without error. Direct inspection of
	// the dialer's resolver isn't possible through the Transport API.
}

func TestDefaultTransport(t *testing.T) {
	tr := DefaultTransport()
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
}

func TestTransportWithTimeout(t *testing.T) {
	tr := TransportWithTimeout(5 * time.Second)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.ResponseHeaderTimeout != 5*time.Second {
		t.Errorf("expected ResponseHeaderTimeout 5s, got %v", tr.ResponseHeaderTimeout)
	}
}

func TestTransportPool(t *testing.T) {
	pool := NewTransportPool()

	// Default transport returned for unknown host
	tr := pool.Get("unknown.host")
	if tr != pool.defaultTransport {
		t.Error("expected default transport for unknown host")
	}

	// Set custom transport for host
	cfg := DefaultTransportConfig
	cfg.MaxIdleConns = 42
	pool.SetForHost("custom.host", cfg)

	tr = pool.Get("custom.host")
	if tr.MaxIdleConns != 42 {
		t.Errorf("expected MaxIdleConns 42 for custom host, got %d", tr.MaxIdleConns)
	}

	// CloseIdleConnections should not panic
	pool.CloseIdleConnections()
}
