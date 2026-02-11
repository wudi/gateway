package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/wudi/gateway/internal/config"
)

// TransportConfig configures the HTTP transport
type TransportConfig struct {
	// Connection settings
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int
	IdleConnTimeout     time.Duration

	// Timeouts
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration
	ExpectContinueTimeout time.Duration

	// TLS settings
	InsecureSkipVerify bool
	CAFile             string

	// Keep-alive
	DisableKeepAlives bool

	// HTTP/2
	ForceHTTP2 bool

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
	ForceHTTP2:            true,
}

// NewTransport creates a new HTTP transport with the given configuration
func NewTransport(cfg TransportConfig) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: 30 * time.Second,
		Resolver:  cfg.Resolver,
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	// Load custom CA file if specified
	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err == nil {
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM(caCert)
			tlsConfig.RootCAs = pool
		}
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ExpectContinueTimeout: cfg.ExpectContinueTimeout,
		DisableKeepAlives:     cfg.DisableKeepAlives,
		TLSClientConfig:       tlsConfig,
		ForceAttemptHTTP2:     cfg.ForceHTTP2,
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

// MergeTransportConfigs applies non-zero values from config.TransportConfig overlays
// onto a base proxy.TransportConfig. Overlays are applied in order (later overrides earlier).
func MergeTransportConfigs(base TransportConfig, overlays ...config.TransportConfig) TransportConfig {
	for _, o := range overlays {
		if o.MaxIdleConns > 0 {
			base.MaxIdleConns = o.MaxIdleConns
		}
		if o.MaxIdleConnsPerHost > 0 {
			base.MaxIdleConnsPerHost = o.MaxIdleConnsPerHost
		}
		if o.MaxConnsPerHost > 0 {
			base.MaxConnsPerHost = o.MaxConnsPerHost
		}
		if o.IdleConnTimeout > 0 {
			base.IdleConnTimeout = o.IdleConnTimeout
		}
		if o.DialTimeout > 0 {
			base.DialTimeout = o.DialTimeout
		}
		if o.TLSHandshakeTimeout > 0 {
			base.TLSHandshakeTimeout = o.TLSHandshakeTimeout
		}
		if o.ResponseHeaderTimeout > 0 {
			base.ResponseHeaderTimeout = o.ResponseHeaderTimeout
		}
		if o.ExpectContinueTimeout > 0 {
			base.ExpectContinueTimeout = o.ExpectContinueTimeout
		}
		if o.DisableKeepAlives {
			base.DisableKeepAlives = true
		}
		if o.InsecureSkipVerify {
			base.InsecureSkipVerify = true
		}
		if o.CAFile != "" {
			base.CAFile = o.CAFile
		}
		if o.ForceHTTP2 != nil {
			base.ForceHTTP2 = *o.ForceHTTP2
		}
	}
	return base
}

// TransportPool manages a pool of transports keyed by upstream name.
type TransportPool struct {
	defaultTransport *http.Transport
	transports       map[string]*http.Transport
}

// NewTransportPool creates a new transport pool with a default transport.
func NewTransportPool() *TransportPool {
	return &TransportPool{
		defaultTransport: DefaultTransport(),
		transports:       make(map[string]*http.Transport),
	}
}

// NewTransportPoolWithDefault creates a new transport pool with a custom default config.
func NewTransportPoolWithDefault(cfg TransportConfig) *TransportPool {
	return &TransportPool{
		defaultTransport: NewTransport(cfg),
		transports:       make(map[string]*http.Transport),
	}
}

// Get returns a transport for the given upstream name.
// Returns the default transport for empty or unknown names.
func (tp *TransportPool) Get(name string) *http.Transport {
	if name != "" {
		if t, ok := tp.transports[name]; ok {
			return t
		}
	}
	return tp.defaultTransport
}

// Set adds a named transport built from the given config.
func (tp *TransportPool) Set(name string, cfg TransportConfig) {
	tp.transports[name] = NewTransport(cfg)
}

// SetForHost sets a custom transport for a host (legacy API, delegates to Set).
func (tp *TransportPool) SetForHost(host string, cfg TransportConfig) {
	tp.Set(host, cfg)
}

// Names returns the upstream names that have custom transports.
func (tp *TransportPool) Names() []string {
	names := make([]string, 0, len(tp.transports))
	for name := range tp.transports {
		names = append(names, name)
	}
	return names
}

// DefaultConfig returns the TransportConfig that produced the default transport.
// This is approximate — we return the current default fields for admin display.
func (tp *TransportPool) DefaultConfig() map[string]interface{} {
	dt := tp.defaultTransport
	return map[string]interface{}{
		"max_idle_conns":          dt.MaxIdleConns,
		"max_idle_conns_per_host": dt.MaxIdleConnsPerHost,
		"max_conns_per_host":      dt.MaxConnsPerHost,
		"idle_conn_timeout":       fmt.Sprintf("%v", dt.IdleConnTimeout),
		"tls_handshake_timeout":   fmt.Sprintf("%v", dt.TLSHandshakeTimeout),
		"response_header_timeout": fmt.Sprintf("%v", dt.ResponseHeaderTimeout),
		"expect_continue_timeout": fmt.Sprintf("%v", dt.ExpectContinueTimeout),
		"disable_keep_alives":     dt.DisableKeepAlives,
		"force_attempt_http2":     dt.ForceAttemptHTTP2,
	}
}

// CloseIdleConnections closes idle connections on all transports
func (tp *TransportPool) CloseIdleConnections() {
	tp.defaultTransport.CloseIdleConnections()
	for _, t := range tp.transports {
		t.CloseIdleConnections()
	}
}
