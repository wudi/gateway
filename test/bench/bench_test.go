//go:build integration

package bench

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/runway"
)

// connTracker wraps a net.Listener to count unique TCP connections.
type connTracker struct {
	net.Listener
	conns sync.Map
	count atomic.Int64
}

func newConnTracker(lis net.Listener) *connTracker {
	return &connTracker{Listener: lis}
}

func (ct *connTracker) Accept() (net.Conn, error) {
	conn, err := ct.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if _, loaded := ct.conns.LoadOrStore(conn.RemoteAddr().String(), struct{}{}); !loaded {
		ct.count.Add(1)
	}
	return conn, nil
}

func (ct *connTracker) UniqueConns() int64 {
	return ct.count.Load()
}

// newTestRunway creates a Runway instance from config, returning the handler.
// The caller must call the returned cleanup function.
func newTestRunway(tb testing.TB, cfg *config.Config) (http.Handler, func()) {
	tb.Helper()

	gw, err := runway.New(cfg)
	if err != nil {
		tb.Fatalf("Failed to create runway: %v", err)
	}
	return gw.Handler(), func() { gw.Close() }
}

// baseConfig returns a minimal config skeleton.
func baseConfig() *config.Config {
	return &config.Config{
		Listeners: []config.ListenerConfig{{
			ID:       "bench-http",
			Address:  ":0",
			Protocol: config.ProtocolHTTP,
			HTTP: config.HTTPListenerConfig{
				ReadTimeout:  30 * time.Second,
				WriteTimeout: 30 * time.Second,
			},
		}},
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Admin: config.AdminConfig{
			Enabled: false,
		},
	}
}

// newHTTPEchoBackend creates an httptest.Server that echoes back a JSON response.
// The returned connTracker tracks unique TCP connections to the backend.
func newHTTPEchoBackend(tb testing.TB) (*httptest.Server, *connTracker) {
	tb.Helper()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","path":"` + r.URL.Path + `"}`))
	})

	// Create the server manually so we can wrap the listener.
	server := httptest.NewUnstartedServer(handler)
	tracker := newConnTracker(server.Listener)
	server.Listener = tracker
	server.Start()

	tb.Cleanup(server.Close)
	return server, tracker
}
