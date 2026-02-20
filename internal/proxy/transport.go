package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware/ssrf"
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
	CertFile           string
	KeyFile            string

	// Keep-alive
	DisableKeepAlives bool

	// HTTP/2
	ForceHTTP2 bool

	// HTTP/3
	EnableHTTP3 bool

	// DNS
	Resolver *net.Resolver // nil = default OS resolver

	// SSRF protection
	SSRFProtection *config.SSRFProtectionConfig
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

// buildTLSConfig creates a shared TLS configuration from transport settings.
func buildTLSConfig(cfg TransportConfig) *tls.Config {
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

	// Load client certificate for upstream mTLS
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err == nil {
			tlsConfig.Certificates = []tls.Certificate{cert}
		}
	}

	return tlsConfig
}

// NewTransport creates a new HTTP transport with the given configuration
func NewTransport(cfg TransportConfig) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: 30 * time.Second,
		Resolver:  cfg.Resolver,
	}

	tlsConfig := buildTLSConfig(cfg)

	dialCtx := dialer.DialContext
	if cfg.SSRFProtection != nil && cfg.SSRFProtection.Enabled {
		if sd, err := ssrf.New(dialer, *cfg.SSRFProtection); err == nil {
			dialCtx = sd.DialContext
		}
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialCtx,
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

// NewHTTP3Transport creates an HTTP/3 QUIC transport with the given configuration.
func NewHTTP3Transport(cfg TransportConfig) *http3.Transport {
	tlsConfig := buildTLSConfig(cfg)
	return &http3.Transport{
		TLSClientConfig: tlsConfig,
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
		if o.CertFile != "" {
			base.CertFile = o.CertFile
		}
		if o.KeyFile != "" {
			base.KeyFile = o.KeyFile
		}
		if o.ForceHTTP2 != nil {
			base.ForceHTTP2 = *o.ForceHTTP2
		}
		if o.EnableHTTP3 != nil {
			base.EnableHTTP3 = *o.EnableHTTP3
		}
	}
	return base
}

// WithSSRFProtection returns a copy of the config with SSRF protection applied.
func (tc TransportConfig) WithSSRFProtection(cfg *config.SSRFProtectionConfig) TransportConfig {
	tc.SSRFProtection = cfg
	return tc
}

// TransportPool manages a pool of transports keyed by upstream name.
type TransportPool struct {
	defaultTransport http.RoundTripper
	transports       map[string]http.RoundTripper
}

// NewTransportPool creates a new transport pool with a default transport.
func NewTransportPool() *TransportPool {
	return &TransportPool{
		defaultTransport: DefaultTransport(),
		transports:       make(map[string]http.RoundTripper),
	}
}

// NewTransportPoolWithDefault creates a new transport pool with a custom default config.
func NewTransportPoolWithDefault(cfg TransportConfig) *TransportPool {
	return &TransportPool{
		defaultTransport: NewTransport(cfg),
		transports:       make(map[string]http.RoundTripper),
	}
}

// Get returns a transport for the given upstream name.
// Returns the default transport for empty or unknown names.
func (tp *TransportPool) Get(name string) http.RoundTripper {
	if name != "" {
		if t, ok := tp.transports[name]; ok {
			return t
		}
	}
	return tp.defaultTransport
}

// Set adds a named transport built from the given config.
// Uses HTTP/3 transport when EnableHTTP3 is set, otherwise TCP-based transport.
func (tp *TransportPool) Set(name string, cfg TransportConfig) {
	if cfg.EnableHTTP3 {
		tp.transports[name] = NewHTTP3Transport(cfg)
	} else {
		tp.transports[name] = NewTransport(cfg)
	}
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
	if dt, ok := tp.defaultTransport.(*http.Transport); ok {
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
	// HTTP/3 transport — fewer configurable fields
	return map[string]interface{}{
		"type": "http3",
	}
}

// CloseIdleConnections closes idle connections on all transports
func (tp *TransportPool) CloseIdleConnections() {
	closeIdle(tp.defaultTransport)
	for _, t := range tp.transports {
		closeIdle(t)
	}
}

// closeIdle closes idle connections on a transport using type switch.
func closeIdle(rt http.RoundTripper) {
	switch t := rt.(type) {
	case *http.Transport:
		t.CloseIdleConnections()
	case *http3.Transport:
		t.Close()
	}
}
