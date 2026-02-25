package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

// generateTestCerts creates a self-signed CA and leaf cert for testing.
func generateTestCerts(t *testing.T, dir string) (caFile, certFile, keyFile string) {
	t.Helper()

	// Generate CA key
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "Test CA"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	caFile = filepath.Join(dir, "ca.pem")
	writePEM(t, caFile, "CERTIFICATE", caDER)

	// Generate leaf key + cert signed by CA
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "test-node"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(1 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	certFile = filepath.Join(dir, "cert.pem")
	writePEM(t, certFile, "CERTIFICATE", leafDER)

	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	keyFile = filepath.Join(dir, "key.pem")
	writePEM(t, keyFile, "EC PRIVATE KEY", keyDER)

	return caFile, certFile, keyFile
}

func writePEM(t *testing.T, path, blockType string, data []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: data}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildCPTLSConfig(t *testing.T) {
	dir := t.TempDir()
	caFile, certFile, keyFile := generateTestCerts(t, dir)

	cfg := config.ControlPlaneConfig{
		Address: ":9443",
		TLS: config.TLSConfig{
			Enabled:      true,
			CertFile:     certFile,
			KeyFile:      keyFile,
			ClientCAFile: caFile,
		},
	}

	tlsCfg, err := BuildCPTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildCPTLSConfig failed: %v", err)
	}

	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("expected 1 certificate, got %d", len(tlsCfg.Certificates))
	}
	if tlsCfg.ClientCAs == nil {
		t.Error("ClientCAs should be set")
	}
	if tlsCfg.ClientAuth != 4 { // tls.RequireAndVerifyClientCert = 4
		t.Errorf("ClientAuth should be RequireAndVerifyClientCert, got %d", tlsCfg.ClientAuth)
	}
	if tlsCfg.MinVersion != 0x0304 { // tls.VersionTLS13
		t.Errorf("MinVersion should be TLS 1.3, got %d", tlsCfg.MinVersion)
	}
}

func TestBuildDPTLSConfig(t *testing.T) {
	dir := t.TempDir()
	caFile, certFile, keyFile := generateTestCerts(t, dir)

	cfg := config.DataPlaneConfig{
		Address: "cp:9443",
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
			CAFile:   caFile,
		},
	}

	tlsCfg, err := BuildDPTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildDPTLSConfig failed: %v", err)
	}

	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("expected 1 certificate, got %d", len(tlsCfg.Certificates))
	}
	if tlsCfg.RootCAs == nil {
		t.Error("RootCAs should be set")
	}
	if tlsCfg.MinVersion != 0x0304 { // tls.VersionTLS13
		t.Errorf("MinVersion should be TLS 1.3, got %d", tlsCfg.MinVersion)
	}
}

func TestBuildCPTLSConfigErrors(t *testing.T) {
	t.Run("missing_cert", func(t *testing.T) {
		cfg := config.ControlPlaneConfig{
			TLS: config.TLSConfig{
				CertFile:     "/nonexistent/cert.pem",
				KeyFile:      "/nonexistent/key.pem",
				ClientCAFile: "/nonexistent/ca.pem",
			},
		}
		if _, err := BuildCPTLSConfig(cfg); err == nil {
			t.Fatal("expected error for missing cert")
		}
	})

	t.Run("missing_client_ca", func(t *testing.T) {
		dir := t.TempDir()
		_, certFile, keyFile := generateTestCerts(t, dir)
		cfg := config.ControlPlaneConfig{
			TLS: config.TLSConfig{
				CertFile:     certFile,
				KeyFile:      keyFile,
				ClientCAFile: "/nonexistent/ca.pem",
			},
		}
		if _, err := BuildCPTLSConfig(cfg); err == nil {
			t.Fatal("expected error for missing client CA")
		}
	})
}

func TestBuildDPTLSConfigErrors(t *testing.T) {
	t.Run("missing_cert", func(t *testing.T) {
		cfg := config.DataPlaneConfig{
			TLS: config.TLSConfig{
				CertFile: "/nonexistent/cert.pem",
				KeyFile:  "/nonexistent/key.pem",
				CAFile:   "/nonexistent/ca.pem",
			},
		}
		if _, err := BuildDPTLSConfig(cfg); err == nil {
			t.Fatal("expected error for missing cert")
		}
	})

	t.Run("missing_ca", func(t *testing.T) {
		dir := t.TempDir()
		_, certFile, keyFile := generateTestCerts(t, dir)
		cfg := config.DataPlaneConfig{
			TLS: config.TLSConfig{
				CertFile: certFile,
				KeyFile:  keyFile,
				CAFile:   "/nonexistent/ca.pem",
			},
		}
		if _, err := BuildDPTLSConfig(cfg); err == nil {
			t.Fatal("expected error for missing CA")
		}
	})
}
