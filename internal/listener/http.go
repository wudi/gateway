package listener

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/acme"
)

// HTTPListener wraps an HTTP server as a Listener
type HTTPListener struct {
	id          string
	address     string
	server      *http.Server
	handler     http.Handler
	tlsCfg      *tls.Config
	listener    net.Listener
	certPtr     atomic.Pointer[tls.Certificate] // for hot TLS cert reload
	enableHTTP3 bool
	http3Server *http3.Server
	udpConn     net.PacketConn
	acmeMgr     *acme.Manager // ACME certificate manager (nil if manual TLS)
}

// HTTPListenerConfig holds configuration for creating an HTTP listener
type HTTPListenerConfig struct {
	ID                string
	Address           string
	Handler           http.Handler
	TLS               config.TLSConfig
	ACME              config.ACMEConfig
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	MaxHeaderBytes    int
	ReadHeaderTimeout time.Duration
	EnableHTTP3       bool
}

// NewHTTPListener creates a new HTTP listener
func NewHTTPListener(cfg HTTPListenerConfig) (*HTTPListener, error) {
	h := &HTTPListener{
		id:          cfg.ID,
		address:     cfg.Address,
		handler:     cfg.Handler,
		enableHTTP3: cfg.EnableHTTP3,
	}

	// Set up TLS if enabled
	if cfg.TLS.Enabled {
		if cfg.ACME.Enabled {
			// ACME mode: automatic certificate provisioning
			acmeMgr, err := acme.New(cfg.ACME)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize ACME: %w", err)
			}
			h.acmeMgr = acmeMgr
			h.tlsCfg = acmeMgr.TLSConfig()
		} else {
			// Manual TLS mode
			cert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("failed to load TLS certificates: %w", err)
			}

			h.certPtr.Store(&cert)

			h.tlsCfg = &tls.Config{
				GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
					return h.certPtr.Load(), nil
				},
				MinVersion: tls.VersionTLS12,
			}
		}

		// mTLS: Configure client certificate authentication (applies to both ACME and manual)
		if cfg.TLS.ClientAuth != "" {
			switch cfg.TLS.ClientAuth {
			case "request":
				h.tlsCfg.ClientAuth = tls.RequestClientCert
			case "require":
				h.tlsCfg.ClientAuth = tls.RequireAnyClientCert
			case "verify":
				h.tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
			default:
				h.tlsCfg.ClientAuth = tls.NoClientCert
			}

			// Load client CA if specified
			if cfg.TLS.ClientCAFile != "" {
				caCert, err := os.ReadFile(cfg.TLS.ClientCAFile)
				if err != nil {
					return nil, fmt.Errorf("failed to read client CA file: %w", err)
				}
				caPool := x509.NewCertPool()
				if !caPool.AppendCertsFromPEM(caCert) {
					return nil, fmt.Errorf("failed to parse client CA certificate")
				}
				h.tlsCfg.ClientCAs = caPool
			}
		}
	}

	// Apply defaults
	readTimeout := cfg.ReadTimeout
	if readTimeout == 0 {
		readTimeout = 30 * time.Second
	}

	writeTimeout := cfg.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = 30 * time.Second
	}

	idleTimeout := cfg.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 60 * time.Second
	}

	maxHeaderBytes := cfg.MaxHeaderBytes
	if maxHeaderBytes == 0 {
		maxHeaderBytes = 1 << 20 // 1MB
	}

	readHeaderTimeout := cfg.ReadHeaderTimeout
	if readHeaderTimeout == 0 {
		readHeaderTimeout = 10 * time.Second
	}

	h.server = &http.Server{
		Addr:              cfg.Address,
		Handler:           cfg.Handler,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		ReadHeaderTimeout: readHeaderTimeout,
		TLSConfig:         h.tlsCfg,
	}

	// Set up HTTP/3 server if enabled
	if cfg.EnableHTTP3 && h.tlsCfg != nil {
		h.http3Server = &http3.Server{
			Handler:   cfg.Handler,
			TLSConfig: http3.ConfigureTLSConfig(h.tlsCfg),
		}
	}

	return h, nil
}

// ID returns the listener ID
func (h *HTTPListener) ID() string {
	return h.id
}

// Protocol returns "http"
func (h *HTTPListener) Protocol() string {
	return "http"
}

// Addr returns the address
func (h *HTTPListener) Addr() string {
	return h.address
}

// Start starts the HTTP listener
func (h *HTTPListener) Start(ctx context.Context) error {
	// Start ACME HTTP-01 challenge server if needed
	if h.acmeMgr != nil {
		if err := h.acmeMgr.StartHTTPChallenge(ctx); err != nil {
			return fmt.Errorf("failed to start ACME HTTP challenge server: %w", err)
		}
	}

	ln, err := net.Listen("tcp", h.address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", h.address, err)
	}
	h.listener = ln

	if h.tlsCfg != nil {
		h.listener = tls.NewListener(ln, h.tlsCfg)
	}

	errCh := make(chan error, 2)
	go func() {
		if err := h.server.Serve(h.listener); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Start HTTP/3 QUIC listener on the same port via UDP
	if h.http3Server != nil {
		udpConn, err := net.ListenPacket("udp", h.address)
		if err != nil {
			// Shut down the TCP listener since we failed to start UDP
			h.server.Shutdown(ctx)
			return fmt.Errorf("failed to listen UDP for HTTP/3 on %s: %w", h.address, err)
		}
		h.udpConn = udpConn

		go func() {
			if err := h.http3Server.Serve(udpConn); err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}()
	}

	// Check for immediate startup errors
	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
		return nil
	}
}

// Stop stops the HTTP listener
func (h *HTTPListener) Stop(ctx context.Context) error {
	// Shut down ACME HTTP challenge server
	if h.acmeMgr != nil {
		h.acmeMgr.Stop(ctx)
	}

	// Shut down HTTP/3 first
	if h.http3Server != nil {
		h.http3Server.Close()
	}
	if h.udpConn != nil {
		h.udpConn.Close()
	}

	return h.server.Shutdown(ctx)
}

// ReloadTLSCert hot-swaps the TLS certificate without restarting the listener.
func (h *HTTPListener) ReloadTLSCert(certFile, keyFile string) error {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fmt.Errorf("failed to load TLS certificates: %w", err)
	}
	h.certPtr.Store(&cert)
	return nil
}

// HTTP3Enabled returns whether HTTP/3 is enabled on this listener.
func (h *HTTPListener) HTTP3Enabled() bool {
	return h.enableHTTP3
}

// Server returns the underlying HTTP server
func (h *HTTPListener) Server() *http.Server {
	return h.server
}

// ACMEManager returns the ACME manager, or nil if manual TLS is used.
func (h *HTTPListener) ACMEManager() *acme.Manager {
	return h.acmeMgr
}

// CertPtr returns the manual TLS certificate pointer (for expiry monitoring of manual certs).
func (h *HTTPListener) CertPtr() *tls.Certificate {
	return h.certPtr.Load()
}
