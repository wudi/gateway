package proxy

import (
	"net"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
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

func TestNewTransportPoolWithDefault(t *testing.T) {
	cfg := DefaultTransportConfig
	cfg.MaxIdleConns = 200
	pool := NewTransportPoolWithDefault(cfg)

	tr := pool.Get("")
	if tr.MaxIdleConns != 200 {
		t.Errorf("expected MaxIdleConns 200 from custom default, got %d", tr.MaxIdleConns)
	}
}

func TestTransportPoolSet(t *testing.T) {
	pool := NewTransportPool()
	cfg := DefaultTransportConfig
	cfg.MaxIdleConns = 50
	pool.Set("my-upstream", cfg)

	tr := pool.Get("my-upstream")
	if tr.MaxIdleConns != 50 {
		t.Errorf("expected MaxIdleConns 50, got %d", tr.MaxIdleConns)
	}

	// Unknown upstream returns default
	def := pool.Get("other")
	if def.MaxIdleConns != 100 {
		t.Errorf("expected default MaxIdleConns 100 for unknown upstream, got %d", def.MaxIdleConns)
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
	if dc["max_idle_conns"] != 100 {
		t.Errorf("expected max_idle_conns=100, got %v", dc["max_idle_conns"])
	}
	if dc["force_attempt_http2"] != true {
		t.Errorf("expected force_attempt_http2=true, got %v", dc["force_attempt_http2"])
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
	if merged.ForceHTTP2 != true {
		t.Errorf("expected ForceHTTP2 unchanged at true, got %v", merged.ForceHTTP2)
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
