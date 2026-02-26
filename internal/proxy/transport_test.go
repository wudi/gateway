package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/wudi/runway/config"
)

func TestNewTransportDefault(t *testing.T) {
	tr := NewTransport(DefaultTransportConfig)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.MaxIdleConns != 512 {
		t.Errorf("expected MaxIdleConns 512, got %d", tr.MaxIdleConns)
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
	httpTr, ok := tr.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport for TCP transport")
	}
	if httpTr.MaxIdleConns != 42 {
		t.Errorf("expected MaxIdleConns 42 for custom host, got %d", httpTr.MaxIdleConns)
	}

	// CloseIdleConnections should not panic
	pool.CloseIdleConnections()
}

func TestNewTransportPoolWithDefault(t *testing.T) {
	cfg := DefaultTransportConfig
	cfg.MaxIdleConns = 200
	pool := NewTransportPoolWithDefault(cfg)

	tr := pool.Get("")
	httpTr, ok := tr.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if httpTr.MaxIdleConns != 200 {
		t.Errorf("expected MaxIdleConns 200 from custom default, got %d", httpTr.MaxIdleConns)
	}
}

func TestTransportPoolSet(t *testing.T) {
	pool := NewTransportPool()
	cfg := DefaultTransportConfig
	cfg.MaxIdleConns = 50
	pool.Set("my-upstream", cfg)

	tr := pool.Get("my-upstream")
	httpTr, ok := tr.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if httpTr.MaxIdleConns != 50 {
		t.Errorf("expected MaxIdleConns 50, got %d", httpTr.MaxIdleConns)
	}

	// Unknown upstream returns default
	def := pool.Get("other")
	defHTTP, ok := def.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport for default")
	}
	if defHTTP.MaxIdleConns != 512 {
		t.Errorf("expected default MaxIdleConns 512 for unknown upstream, got %d", defHTTP.MaxIdleConns)
	}
}

func TestTransportPoolNames(t *testing.T) {
	pool := NewTransportPool()
	pool.Set("a", DefaultTransportConfig)
	pool.Set("b", DefaultTransportConfig)

	names := pool.Names()
	if len(names) != 2 {
		t.Errorf("expected 2 names, got %d", len(names))
	}
	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["a"] || !nameSet["b"] {
		t.Errorf("expected names [a, b], got %v", names)
	}
}

func TestTransportPoolDefaultConfig(t *testing.T) {
	pool := NewTransportPool()
	dc := pool.DefaultConfig()
	if dc["max_idle_conns"] != 512 {
		t.Errorf("expected max_idle_conns=512, got %v", dc["max_idle_conns"])
	}
	if dc["force_attempt_http2"] != false {
		t.Errorf("expected force_attempt_http2=false, got %v", dc["force_attempt_http2"])
	}
}

func TestMergeTransportConfigs(t *testing.T) {
	base := DefaultTransportConfig

	// Apply a partial overlay
	overlay := config.TransportConfig{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 50,
		DialTimeout:         5 * time.Second,
	}

	merged := MergeTransportConfigs(base, overlay)

	if merged.MaxIdleConns != 200 {
		t.Errorf("expected MaxIdleConns 200, got %d", merged.MaxIdleConns)
	}
	if merged.MaxIdleConnsPerHost != 50 {
		t.Errorf("expected MaxIdleConnsPerHost 50, got %d", merged.MaxIdleConnsPerHost)
	}
	if merged.DialTimeout != 5*time.Second {
		t.Errorf("expected DialTimeout 5s, got %v", merged.DialTimeout)
	}
	// Fields not in overlay should stay at default
	if merged.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("expected TLSHandshakeTimeout unchanged at 10s, got %v", merged.TLSHandshakeTimeout)
	}
	if merged.ForceHTTP2 != false {
		t.Errorf("expected ForceHTTP2 unchanged at false, got %v", merged.ForceHTTP2)
	}
}

func TestMergeTransportConfigsMultipleOverlays(t *testing.T) {
	base := DefaultTransportConfig

	overlay1 := config.TransportConfig{
		MaxIdleConns: 200,
		CAFile:       "/tmp/ca.pem",
	}
	boolFalse := false
	overlay2 := config.TransportConfig{
		MaxIdleConns: 300, // overrides overlay1
		ForceHTTP2:   &boolFalse,
	}

	merged := MergeTransportConfigs(base, overlay1, overlay2)

	if merged.MaxIdleConns != 300 {
		t.Errorf("expected MaxIdleConns 300 (second overlay wins), got %d", merged.MaxIdleConns)
	}
	if merged.CAFile != "/tmp/ca.pem" {
		t.Errorf("expected CAFile from overlay1, got %q", merged.CAFile)
	}
	if merged.ForceHTTP2 != false {
		t.Errorf("expected ForceHTTP2=false from overlay2, got %v", merged.ForceHTTP2)
	}
}

func TestMergeTransportConfigsBoolFields(t *testing.T) {
	base := DefaultTransportConfig

	overlay := config.TransportConfig{
		DisableKeepAlives:  true,
		InsecureSkipVerify: true,
	}

	merged := MergeTransportConfigs(base, overlay)

	if !merged.DisableKeepAlives {
		t.Error("expected DisableKeepAlives=true")
	}
	if !merged.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true")
	}
}

func TestMergeTransportConfigsCertFiles(t *testing.T) {
	base := DefaultTransportConfig

	overlay := config.TransportConfig{
		CertFile: "/tmp/client.crt",
		KeyFile:  "/tmp/client.key",
	}

	merged := MergeTransportConfigs(base, overlay)

	if merged.CertFile != "/tmp/client.crt" {
		t.Errorf("expected CertFile=/tmp/client.crt, got %q", merged.CertFile)
	}
	if merged.KeyFile != "/tmp/client.key" {
		t.Errorf("expected KeyFile=/tmp/client.key, got %q", merged.KeyFile)
	}

	// Second overlay overrides first
	overlay2 := config.TransportConfig{
		CertFile: "/tmp/other.crt",
		KeyFile:  "/tmp/other.key",
	}
	merged = MergeTransportConfigs(base, overlay, overlay2)
	if merged.CertFile != "/tmp/other.crt" {
		t.Errorf("expected CertFile=/tmp/other.crt, got %q", merged.CertFile)
	}
	if merged.KeyFile != "/tmp/other.key" {
		t.Errorf("expected KeyFile=/tmp/other.key, got %q", merged.KeyFile)
	}
}

func TestNewTransportClientCert(t *testing.T) {
	// Create temporary self-signed cert and key for testing
	dir := t.TempDir()
	certFile := dir + "/client.crt"
	keyFile := dir + "/client.key"

	// Generate a self-signed cert using crypto/ecdsa
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultTransportConfig
	cfg.CertFile = certFile
	cfg.KeyFile = keyFile

	tr := NewTransport(cfg)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.TLSClientConfig == nil {
		t.Fatal("expected non-nil TLSClientConfig")
	}
	if len(tr.TLSClientConfig.Certificates) != 1 {
		t.Fatalf("expected 1 certificate, got %d", len(tr.TLSClientConfig.Certificates))
	}
}

func TestNewTransportClientCertInvalidFiles(t *testing.T) {
	// With invalid paths, the transport should still be created (cert loading silently fails)
	cfg := DefaultTransportConfig
	cfg.CertFile = "/nonexistent/client.crt"
	cfg.KeyFile = "/nonexistent/client.key"

	tr := NewTransport(cfg)
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if len(tr.TLSClientConfig.Certificates) != 0 {
		t.Errorf("expected no certificates with invalid files, got %d", len(tr.TLSClientConfig.Certificates))
	}
}

func TestNewTransportForceHTTP2(t *testing.T) {
	cfg := DefaultTransportConfig
	cfg.ForceHTTP2 = false
	tr := NewTransport(cfg)
	if tr.ForceAttemptHTTP2 {
		t.Error("expected ForceAttemptHTTP2=false")
	}

	cfg.ForceHTTP2 = true
	tr = NewTransport(cfg)
	if !tr.ForceAttemptHTTP2 {
		t.Error("expected ForceAttemptHTTP2=true")
	}
}

func TestNewHTTP3Transport(t *testing.T) {
	cfg := DefaultTransportConfig
	cfg.EnableHTTP3 = true

	h3 := NewHTTP3Transport(cfg)
	if h3 == nil {
		t.Fatal("expected non-nil http3.Transport")
	}
	if h3.TLSClientConfig == nil {
		t.Fatal("expected non-nil TLSClientConfig on HTTP/3 transport")
	}
}

func TestTransportPoolSetHTTP3(t *testing.T) {
	pool := NewTransportPool()

	cfg := DefaultTransportConfig
	cfg.EnableHTTP3 = true
	pool.Set("h3-upstream", cfg)

	tr := pool.Get("h3-upstream")
	if _, ok := tr.(*http3.Transport); !ok {
		t.Errorf("expected *http3.Transport, got %T", tr)
	}

	// TCP upstream still works
	tcpCfg := DefaultTransportConfig
	pool.Set("tcp-upstream", tcpCfg)
	tcpTr := pool.Get("tcp-upstream")
	if _, ok := tcpTr.(*http.Transport); !ok {
		t.Errorf("expected *http.Transport, got %T", tcpTr)
	}
}

func TestTransportPoolCloseIdleWithMixedTypes(t *testing.T) {
	pool := NewTransportPool()

	h3Cfg := DefaultTransportConfig
	h3Cfg.EnableHTTP3 = true
	pool.Set("h3", h3Cfg)
	pool.Set("tcp", DefaultTransportConfig)

	// Should not panic with mixed transport types
	pool.CloseIdleConnections()
}

func TestTransportPoolDefaultConfigHTTP3(t *testing.T) {
	// When default transport is HTTP/3, DefaultConfig should return type info
	h3Transport := NewHTTP3Transport(DefaultTransportConfig)
	pool := &TransportPool{
		defaultTransport: h3Transport,
		transports:       make(map[string]http.RoundTripper),
	}

	dc := pool.DefaultConfig()
	if dc["type"] != "http3" {
		t.Errorf("expected type=http3 for HTTP/3 default transport, got %v", dc["type"])
	}
}

func TestMergeTransportConfigsEnableHTTP3(t *testing.T) {
	base := DefaultTransportConfig

	boolTrue := true
	overlay := config.TransportConfig{
		EnableHTTP3: &boolTrue,
	}

	merged := MergeTransportConfigs(base, overlay)
	if !merged.EnableHTTP3 {
		t.Error("expected EnableHTTP3=true after merge")
	}

	// EnableHTTP3 can be overridden to false
	boolFalse := false
	overlay2 := config.TransportConfig{
		EnableHTTP3: &boolFalse,
	}
	merged = MergeTransportConfigs(base, overlay, overlay2)
	if merged.EnableHTTP3 {
		t.Error("expected EnableHTTP3=false after second overlay")
	}
}
