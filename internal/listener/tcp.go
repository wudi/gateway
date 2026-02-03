package listener

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/proxy/tcp"
)

// TCPListener handles TCP connections
type TCPListener struct {
	id            string
	address       string
	listener      net.Listener
	proxy         *tcp.Proxy
	tlsCfg        *tls.Config
	sniRouting    bool
	idleTimeout   time.Duration
	activeConns   int64
	connWg        sync.WaitGroup
	closeCh       chan struct{}
	closeOnce     sync.Once
}

// TCPListenerConfig holds configuration for creating a TCP listener
type TCPListenerConfig struct {
	ID           string
	Address      string
	Proxy        *tcp.Proxy
	TLS          config.TLSConfig
	SNIRouting   bool
	IdleTimeout  time.Duration
}

// NewTCPListener creates a new TCP listener
func NewTCPListener(cfg TCPListenerConfig) (*TCPListener, error) {
	l := &TCPListener{
		id:          cfg.ID,
		address:     cfg.Address,
		proxy:       cfg.Proxy,
		sniRouting:  cfg.SNIRouting,
		idleTimeout: cfg.IdleTimeout,
		closeCh:     make(chan struct{}),
	}

	// Set up TLS if enabled (but don't terminate - just for verification)
	// For SNI routing, we peek at the handshake without terminating
	if cfg.TLS.Enabled && !cfg.SNIRouting {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS certificates: %w", err)
		}

		l.tlsCfg = &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	// Apply defaults
	if l.idleTimeout == 0 {
		l.idleTimeout = 5 * time.Minute
	}

	return l, nil
}

// ID returns the listener ID
func (l *TCPListener) ID() string {
	return l.id
}

// Protocol returns "tcp"
func (l *TCPListener) Protocol() string {
	return "tcp"
}

// Addr returns the address
func (l *TCPListener) Addr() string {
	return l.address
}

// Start starts the TCP listener
func (l *TCPListener) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", l.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", l.address, err)
	}

	// Wrap with TLS if configured (for TLS termination mode)
	if l.tlsCfg != nil {
		ln = tls.NewListener(ln, l.tlsCfg)
	}

	l.listener = ln

	// Start accept loop
	go l.acceptLoop(ctx)

	return nil
}

// acceptLoop accepts incoming connections
func (l *TCPListener) acceptLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.closeCh:
			return
		default:
		}

		// Set accept deadline to allow periodic checking of close channel
		if tcpLn, ok := l.listener.(*net.TCPListener); ok {
			tcpLn.SetDeadline(time.Now().Add(1 * time.Second))
		}

		conn, err := l.listener.Accept()
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}

			select {
			case <-l.closeCh:
				return
			default:
				log.Printf("TCP listener %s accept error: %v", l.id, err)
				continue
			}
		}

		// Track active connections
		atomic.AddInt64(&l.activeConns, 1)
		l.connWg.Add(1)

		// Handle connection in goroutine
		go l.handleConn(ctx, conn)
	}
}

// handleConn handles a single connection
func (l *TCPListener) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		atomic.AddInt64(&l.activeConns, -1)
		l.connWg.Done()
	}()

	// Set idle timeout
	if l.idleTimeout > 0 {
		conn.SetDeadline(time.Now().Add(l.idleTimeout))
	}

	// Delegate to proxy
	if err := l.proxy.Handle(ctx, conn, l.id, l.sniRouting); err != nil {
		log.Printf("TCP proxy error for listener %s: %v", l.id, err)
	}
}

// Stop stops the TCP listener
func (l *TCPListener) Stop(ctx context.Context) error {
	l.closeOnce.Do(func() {
		close(l.closeCh)
	})

	if l.listener != nil {
		l.listener.Close()
	}

	// Wait for active connections to finish with timeout
	done := make(chan struct{})
	go func() {
		l.connWg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("TCP listener %s stopped gracefully", l.id)
	case <-ctx.Done():
		log.Printf("TCP listener %s stop timed out with %d active connections", l.id, atomic.LoadInt64(&l.activeConns))
	}

	return nil
}

// ActiveConnections returns the number of active connections
func (l *TCPListener) ActiveConnections() int64 {
	return atomic.LoadInt64(&l.activeConns)
}
