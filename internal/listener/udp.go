package listener

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/proxy/udp"
)

// UDPListener handles UDP connections
type UDPListener struct {
	id              string
	address         string
	conn            *net.UDPConn
	proxy           *udp.Proxy
	readBufferSize  int
	writeBufferSize int
	wg              sync.WaitGroup
	closeCh         chan struct{}
	closeOnce       sync.Once
}

// UDPListenerConfig holds configuration for creating a UDP listener
type UDPListenerConfig struct {
	ID              string
	Address         string
	Proxy           *udp.Proxy
	UDP             config.UDPListenerConfig
}

// NewUDPListener creates a new UDP listener
func NewUDPListener(cfg UDPListenerConfig) (*UDPListener, error) {
	l := &UDPListener{
		id:              cfg.ID,
		address:         cfg.Address,
		proxy:           cfg.Proxy,
		readBufferSize:  cfg.UDP.ReadBufferSize,
		writeBufferSize: cfg.UDP.WriteBufferSize,
		closeCh:         make(chan struct{}),
	}

	// Apply defaults
	if l.readBufferSize == 0 {
		l.readBufferSize = 4096
	}
	if l.writeBufferSize == 0 {
		l.writeBufferSize = 4096
	}

	return l, nil
}

// ID returns the listener ID
func (l *UDPListener) ID() string {
	return l.id
}

// Protocol returns "udp"
func (l *UDPListener) Protocol() string {
	return "udp"
}

// Addr returns the address
func (l *UDPListener) Addr() string {
	return l.address
}

// Start starts the UDP listener
func (l *UDPListener) Start(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", l.address)
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address %s: %w", l.address, err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP %s: %w", l.address, err)
	}

	// Set buffer sizes
	if l.readBufferSize > 0 {
		if err := conn.SetReadBuffer(l.readBufferSize); err != nil {
			log.Printf("Warning: failed to set UDP read buffer size: %v", err)
		}
	}
	if l.writeBufferSize > 0 {
		if err := conn.SetWriteBuffer(l.writeBufferSize); err != nil {
			log.Printf("Warning: failed to set UDP write buffer size: %v", err)
		}
	}

	l.conn = conn

	// Start serving
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		if err := l.proxy.Serve(ctx, conn, l.id, l.readBufferSize); err != nil {
			select {
			case <-l.closeCh:
				// Expected during shutdown
			default:
				log.Printf("UDP listener %s serve error: %v", l.id, err)
			}
		}
	}()

	return nil
}

// Stop stops the UDP listener
func (l *UDPListener) Stop(ctx context.Context) error {
	l.closeOnce.Do(func() {
		close(l.closeCh)
	})

	if l.conn != nil {
		l.conn.Close()
	}

	// Wait for serving to stop with context timeout
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Printf("UDP listener %s stopped gracefully", l.id)
	case <-ctx.Done():
		log.Printf("UDP listener %s stop timed out", l.id)
	}

	return nil
}

// Conn returns the underlying UDP connection
func (l *UDPListener) Conn() *net.UDPConn {
	return l.conn
}
