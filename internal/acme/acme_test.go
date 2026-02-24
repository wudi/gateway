package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"golang.org/x/crypto/acme/autocert"

	"github.com/wudi/gateway/config"
)

func TestNew_Defaults(t *testing.T) {
	m, err := New(config.ACMEConfig{
		Enabled: true,
		Domains: []string{"example.com"},
		Email:   "admin@example.com",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if m.challengeType != "tls-alpn-01" {
		t.Errorf("default challenge type = %q, want tls-alpn-01", m.challengeType)
	}
	if m.httpAddress != ":80" {
		t.Errorf("default http address = %q, want :80", m.httpAddress)
	}
	// autocertMgr.Client should be nil when using default directory
	if m.autocertMgr.Client != nil {
		t.Errorf("expected nil Client for default directory URL")
	}
}

func TestNew_CustomDirectory(t *testing.T) {
	staging := "https://acme-staging-v02.api.letsencrypt.org/directory"
	m, err := New(config.ACMEConfig{
		Enabled:      true,
		Domains:      []string{"example.com"},
		DirectoryURL: staging,
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	if m.autocertMgr.Client == nil {
		t.Fatal("expected non-nil Client for custom directory URL")
	}
	if m.autocertMgr.Client.DirectoryURL != staging {
		t.Errorf("directory URL = %q, want %q", m.autocertMgr.Client.DirectoryURL, staging)
	}
}

func TestNew_NoDomains(t *testing.T) {
	_, err := New(config.ACMEConfig{
		Enabled: true,
	})
	if err == nil {
		t.Fatal("expected error for empty domains")
	}
}

func TestCertStatus_BeforeHandshake(t *testing.T) {
	m, err := New(config.ACMEConfig{
		Enabled: true,
		Domains: []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	info := m.CertStatus()
	if info.DaysLeft != -1 {
		t.Errorf("DaysLeft before handshake = %d, want -1", info.DaysLeft)
	}
	if len(info.Domains) != 1 || info.Domains[0] != "example.com" {
		t.Errorf("Domains = %v, want [example.com]", info.Domains)
	}
}

func TestUpdateCertInfo(t *testing.T) {
	m, err := New(config.ACMEConfig{
		Enabled: true,
		Domains: []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Create a self-signed cert for testing
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	notBefore := time.Now()
	notAfter := notBefore.Add(90 * 24 * time.Hour)
	serial := big.NewInt(12345)

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "example.com"},
		Issuer:       pkix.Name{CommonName: "Fake CA"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		DNSNames:     []string{"example.com", "www.example.com"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}

	leaf, _ := x509.ParseCertificate(certDER)
	m.updateCertInfo(leaf)

	info := m.CertStatus()
	if info.Issuer != "example.com" { // self-signed, so issuer = subject
		t.Errorf("Issuer = %q, want %q", info.Issuer, "example.com")
	}
	if len(info.Domains) != 2 {
		t.Errorf("Domains count = %d, want 2", len(info.Domains))
	}
	if info.DaysLeft < 89 || info.DaysLeft > 91 {
		t.Errorf("DaysLeft = %d, expected ~90", info.DaysLeft)
	}
	if info.Serial != "3039" { // 12345 in hex
		t.Errorf("Serial = %q, want %q", info.Serial, "3039")
	}
}

func TestTLSConfig_HasGetCertificate(t *testing.T) {
	m, err := New(config.ACMEConfig{
		Enabled: true,
		Domains: []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	tlsCfg := m.TLSConfig()
	if tlsCfg.GetCertificate == nil {
		t.Error("GetCertificate should be set")
	}
	if tlsCfg.MinVersion != 0x0303 { // tls.VersionTLS12
		t.Errorf("MinVersion = %#x, want 0x0303 (TLS 1.2)", tlsCfg.MinVersion)
	}
}

func TestStartHTTPChallenge_NoopForTLSALPN(t *testing.T) {
	m, err := New(config.ACMEConfig{
		Enabled:       true,
		Domains:       []string{"example.com"},
		ChallengeType: "tls-alpn-01",
	})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Should be a no-op (no HTTP server started)
	if err := m.StartHTTPChallenge(t.Context()); err != nil {
		t.Errorf("StartHTTPChallenge() error: %v", err)
	}
	if m.httpServer != nil {
		t.Error("httpServer should be nil for tls-alpn-01")
	}
}

func TestDefaultACMEDirectory(t *testing.T) {
	// Verify autocert.DefaultACMEDirectory is Let's Encrypt production
	if autocert.DefaultACMEDirectory == "" {
		t.Error("DefaultACMEDirectory should not be empty")
	}
}

func TestFormatSerial(t *testing.T) {
	tests := []struct {
		input *big.Int
		want  string
	}{
		{big.NewInt(255), "FF"},
		{big.NewInt(0), "0"},
		{nil, ""},
	}
	for _, tt := range tests {
		got := formatSerial(tt.input)
		if got != tt.want {
			t.Errorf("formatSerial(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
