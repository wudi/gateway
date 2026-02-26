package listener

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

// generateTestCert creates a temporary self-signed certificate for testing.
func generateTestCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	certFile = dir + "/cert.pem"
	keyFile = dir + "/key.pem"

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
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
	return
}

func TestHTTPListenerStartStop(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	l, err := NewHTTPListener(HTTPListenerConfig{
		ID:      "test",
		Address: "127.0.0.1:0",
		Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use a free port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	l.address = addr
	l.server.Addr = addr

	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Verify TCP is accepting connections
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect to listener: %v", err)
	}
	conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Stop(ctx); err != nil {
		t.Fatalf("failed to stop listener: %v", err)
	}
}

func TestHTTPListenerHTTP3Enabled(t *testing.T) {
	certFile, keyFile := generateTestCert(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	l, err := NewHTTPListener(HTTPListenerConfig{
		ID:      "h3-test",
		Address: "127.0.0.1:0",
		Handler: handler,
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
		EnableHTTP3: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !l.HTTP3Enabled() {
		t.Error("expected HTTP3Enabled() to return true")
	}
	if l.http3Server == nil {
		t.Error("expected http3Server to be initialized")
	}
}

func TestHTTPListenerHTTP3StartStop(t *testing.T) {
	certFile, keyFile := generateTestCert(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// Use a free port
	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := tcpLn.Addr().String()
	tcpLn.Close()

	l, err := NewHTTPListener(HTTPListenerConfig{
		ID:      "h3-start-test",
		Address: addr,
		Handler: handler,
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
		EnableHTTP3: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Verify TCP is accepting TLS connections
	tlsConn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 2 * time.Second},
		"tcp", addr,
		&tls.Config{InsecureSkipVerify: true},
	)
	if err != nil {
		t.Fatalf("failed to TLS-connect to listener: %v", err)
	}
	tlsConn.Close()

	// Verify UDP socket was opened (for QUIC)
	if l.udpConn == nil {
		t.Error("expected udpConn to be non-nil after Start()")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Stop(ctx); err != nil {
		t.Fatalf("failed to stop listener: %v", err)
	}
}

func TestHTTPListenerHTTP3DisabledNoUDP(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	l, err := NewHTTPListener(HTTPListenerConfig{
		ID:          "no-h3",
		Address:     "127.0.0.1:0",
		Handler:     handler,
		EnableHTTP3: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	if l.HTTP3Enabled() {
		t.Error("expected HTTP3Enabled() to return false")
	}
	if l.http3Server != nil {
		t.Error("expected http3Server to be nil when HTTP/3 disabled")
	}
}

func TestHTTPListenerHTTP3WithoutTLS(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	// HTTP/3 without TLS should still create the listener (validation happens at config layer)
	// but http3Server should be nil because tlsCfg is nil
	l, err := NewHTTPListener(HTTPListenerConfig{
		ID:          "h3-no-tls",
		Address:     "127.0.0.1:0",
		Handler:     handler,
		EnableHTTP3: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// http3Server requires tlsCfg, so it should be nil
	if l.http3Server != nil {
		t.Error("expected http3Server to be nil without TLS")
	}
}
