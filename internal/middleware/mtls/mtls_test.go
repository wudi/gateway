package mtls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/variables"
)

func newSelfSignedCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
			CommonName:   "test-client",
		},
		Issuer: pkix.Name{
			Organization: []string{"Test CA"},
			CommonName:   "test-ca",
		},
		NotBefore: time.Now().Add(-time.Hour),
		NotAfter:  time.Now().Add(time.Hour),
		DNSNames:  []string{"client.example.com", "alt.example.com"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestMiddleware_WithCert(t *testing.T) {
	cert := newSelfSignedCert(t)

	var captured *variables.CertInfo
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := variables.GetFromRequest(r)
		captured = ctx.CertInfo
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware()(inner)

	r := httptest.NewRequest("GET", "/", nil)
	// Attach a variable context to the request
	varCtx := variables.NewContext(r)
	r = r.WithContext(context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx))
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert},
	}

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if captured == nil {
		t.Fatal("expected CertInfo to be set")
	}

	if !strings.Contains(captured.Subject, "test-client") {
		t.Errorf("expected subject to contain 'test-client', got %q", captured.Subject)
	}

	// Self-signed: issuer == subject
	if !strings.Contains(captured.Issuer, "test-client") {
		t.Errorf("expected issuer to contain 'test-client' (self-signed), got %q", captured.Issuer)
	}

	if captured.SerialNumber != "42" {
		t.Errorf("expected serial number '42', got %q", captured.SerialNumber)
	}

	expectedFP := sha256.Sum256(cert.Raw)
	expectedFPStr := hex.EncodeToString(expectedFP[:])
	if captured.Fingerprint != expectedFPStr {
		t.Errorf("expected fingerprint %q, got %q", expectedFPStr, captured.Fingerprint)
	}

	if len(captured.DNSNames) != 2 || captured.DNSNames[0] != "client.example.com" {
		t.Errorf("expected DNSNames [client.example.com, alt.example.com], got %v", captured.DNSNames)
	}
}

func TestMiddleware_NoCert(t *testing.T) {
	var captured *variables.CertInfo
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := variables.GetFromRequest(r)
		captured = ctx.CertInfo
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware()(inner)

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := variables.NewContext(r)
	r = r.WithContext(context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx))
	// No TLS at all

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if captured != nil {
		t.Error("expected CertInfo to be nil when no TLS")
	}
}

func TestMiddleware_TLSNoPeerCerts(t *testing.T) {
	var captured *variables.CertInfo
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := variables.GetFromRequest(r)
		captured = ctx.CertInfo
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware()(inner)

	r := httptest.NewRequest("GET", "/", nil)
	varCtx := variables.NewContext(r)
	r = r.WithContext(context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx))
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{}, // TLS but no client cert
	}

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if captured != nil {
		t.Error("expected CertInfo to be nil when TLS has no peer certs")
	}
}

func TestMiddleware_CallsNext(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := Middleware()(inner)

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	mw.ServeHTTP(w, r)

	if !called {
		t.Error("middleware did not call next handler")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
