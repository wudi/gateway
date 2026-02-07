package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// TransportConfig configures the HTTP transport
type TransportConfig struct {
	// Connection settings
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int
	IdleConnTimeout     time.Duration

	// Timeouts
	DialTimeout         time.Duration
	TLSHandshakeTimeout time.Duration
	ResponseHeaderTimeout time.Duration
	ExpectContinueTimeout time.Duration

	// TLS settings
	InsecureSkipVerify bool

	// Keep-alive
	DisableKeepAlives bool

	// DNS
	Resolver *net.Resolver // nil = default OS resolver
}

// DefaultTransportConfig provides default transport settings
var DefaultTransportConfig = TransportConfig{
	MaxIdleConns:          100,
	MaxIdleConnsPerHost:   10,
	MaxConnsPerHost:       0, // unlimited
	IdleConnTimeout:       90 * time.Second,
	DialTimeout:           30 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 0, // no timeout
	ExpectContinueTimeout: 1 * time.Second,
	InsecureSkipVerify:    false,
	DisableKeepAlives:     false,
}

// NewTransport creates a new HTTP transport with the given configuration
func NewTransport(cfg TransportConfig) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: 30 * time.Second,
		Resolver:  cfg.Resolver,
	}
	return &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: dialer.DialContext,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: cfg.ExpectContinueTimeout,
		DisableKeepAlives:     cfg.DisableKeepAlives,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.InsecureSkipVerify,
		},
		ForceAttemptHTTP2: true,
	}
}

// DefaultTransport creates a transport with default settings
func DefaultTransport() *http.Transport {
	return NewTransport(DefaultTransportConfig)
}

// TransportWithTimeout creates a transport with a specific timeout
func TransportWithTimeout(timeout time.Duration) *http.Transport {
	cfg := DefaultTransportConfig
	cfg.ResponseHeaderTimeout = timeout
	return NewTransport(cfg)
}

// TransportPool manages a pool of transports
type TransportPool struct {
	defaultTransport *http.Transport
	transports       map[string]*http.Transport
}

// NewTransportPool creates a new transport pool
func NewTransportPool() *TransportPool {
	return &TransportPool{
		defaultTransport: DefaultTransport(),
		transports:       make(map[string]*http.Transport),
	}
}

// Get returns a transport for the given host
func (tp *TransportPool) Get(host string) *http.Transport {
	if t, ok := tp.transports[host]; ok {
		return t
	}
	return tp.defaultTransport
}

// SetForHost sets a custom transport for a host
func (tp *TransportPool) SetForHost(host string, cfg TransportConfig) {
	tp.transports[host] = NewTransport(cfg)
}

// CloseIdleConnections closes idle connections on all transports
func (tp *TransportPool) CloseIdleConnections() {
	tp.defaultTransport.CloseIdleConnections()
	for _, t := range tp.transports {
		t.CloseIdleConnections()
	}
}
