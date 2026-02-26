package clientmtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

// testCA generates a self-signed CA certificate and key.
func testCA(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return cert, key, pemBytes
}

// testClientCert generates a client certificate signed by the given CA.
func testClientCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(100),
		Subject:      pkix.Name{CommonName: "test-client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// testExpiredClientCert generates an expired client certificate signed by the given CA.
func testExpiredClientCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(200),
		Subject:      pkix.Name{CommonName: "expired-client"},
		NotBefore:    time.Now().Add(-48 * time.Hour),
		NotAfter:     time.Now().Add(-24 * time.Hour), // expired
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// testCAWide generates a CA with a wide validity window (covers expired certs from 48h ago).
func testCAWide(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-72 * time.Hour),
		NotAfter:              time.Now().Add(72 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return cert, key, pemBytes
}

func writeCAFile(t *testing.T, dir, name string, pemData []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pemData, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerify_NoCert_VerifyMode_Rejects(t *testing.T) {
	ca, _, caPEM := testCA(t, "Test-CA")
	_ = ca
	dir := t.TempDir()
	caFile := writeCAFile(t, dir, "ca.pem", caPEM)

	v, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// No TLS at all
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}

	// TLS but no peer certs
	r = httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestVerify_CorrectCA_Allows(t *testing.T) {
	ca, caKey, caPEM := testCA(t, "Test-CA")
	dir := t.TempDir()
	caFile := writeCAFile(t, dir, "ca.pem", caPEM)
	clientCert := testClientCert(t, ca, caKey)

	v, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{clientCert},
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("expected handler to be called")
	}
}

func TestVerify_WrongCA_Rejects(t *testing.T) {
	_, _, caPEM := testCA(t, "Trusted-CA")
	wrongCA, wrongKey, _ := testCA(t, "Wrong-CA")
	dir := t.TempDir()
	caFile := writeCAFile(t, dir, "ca.pem", caPEM)
	clientCert := testClientCert(t, wrongCA, wrongKey)

	v, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{clientCert},
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRequest_NoCert_Allows(t *testing.T) {
	v, err := New(config.ClientMTLSConfig{
		Enabled:    true,
		ClientAuth: "request",
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	// No TLS
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("expected handler to be called")
	}
}

func TestRequire_NoCert_Rejects(t *testing.T) {
	v, err := New(config.ClientMTLSConfig{
		Enabled:    true,
		ClientAuth: "require",
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRequire_WithCert_Allows(t *testing.T) {
	ca, caKey, _ := testCA(t, "Any-CA")
	clientCert := testClientCert(t, ca, caKey)

	v, err := New(config.ClientMTLSConfig{
		Enabled:    true,
		ClientAuth: "require",
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{clientCert},
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("expected handler to be called")
	}
}

func TestVerify_MultipleCAs(t *testing.T) {
	ca1, ca1Key, ca1PEM := testCA(t, "CA-One")
	ca2, ca2Key, ca2PEM := testCA(t, "CA-Two")

	dir := t.TempDir()
	ca1File := writeCAFile(t, dir, "ca1.pem", ca1PEM)
	ca2File := writeCAFile(t, dir, "ca2.pem", ca2PEM)

	clientFromCA1 := testClientCert(t, ca1, ca1Key)
	clientFromCA2 := testClientCert(t, ca2, ca2Key)

	v, err := New(config.ClientMTLSConfig{
		Enabled:    true,
		ClientAuth: "verify",
		ClientCAs:  []string{ca1File, ca2File},
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Cert from CA1
	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{clientFromCA1},
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("CA1 cert: expected 200, got %d", w.Code)
	}

	// Cert from CA2
	r = httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{clientFromCA2},
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("CA2 cert: expected 200, got %d", w.Code)
	}
}

func TestMetrics(t *testing.T) {
	ca, caKey, caPEM := testCA(t, "Test-CA")
	wrongCA, wrongKey, _ := testCA(t, "Wrong-CA")
	dir := t.TempDir()
	caFile := writeCAFile(t, dir, "ca.pem", caPEM)

	goodCert := testClientCert(t, ca, caKey)
	badCert := testClientCert(t, wrongCA, wrongKey)

	v, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	mw := v.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Successful verification
	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{goodCert}}
	handler.ServeHTTP(httptest.NewRecorder(), r)

	// Failed verification
	r = httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{badCert}}
	handler.ServeHTTP(httptest.NewRecorder(), r)

	// No cert (rejected)
	r = httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if v.Verified() != 1 {
		t.Errorf("expected 1 verified, got %d", v.Verified())
	}
	if v.Rejected() != 2 {
		t.Errorf("expected 2 rejected, got %d", v.Rejected())
	}
}

func TestAllowExpired(t *testing.T) {
	// CA must have a wide validity window covering the expired cert's entire lifetime
	ca, caKey, caPEM := testCAWide(t, "Test-CA")
	dir := t.TempDir()
	caFile := writeCAFile(t, dir, "ca.pem", caPEM)
	expiredCert := testExpiredClientCert(t, ca, caKey)

	// Without allow_expired: should reject
	v, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	mw := v.Middleware()
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{expiredCert}}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("expected 403 for expired cert, got %d", w.Code)
	}

	// With allow_expired: should allow
	v2, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: caFile,
		AllowExpired: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	mw2 := v2.Middleware()
	handler2 := mw2(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	r = httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{expiredCert}}
	w = httptest.NewRecorder()
	handler2.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Errorf("expected 200 with allow_expired, got %d", w.Code)
	}
}

func TestByRoute(t *testing.T) {
	ca, _, caPEM := testCA(t, "Test-CA")
	_ = ca
	dir := t.TempDir()
	caFile := writeCAFile(t, dir, "ca.pem", caPEM)

	m := NewClientMTLSByRoute()
	err := m.AddRoute("route-1", config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: caFile,
	})
	if err != nil {
		t.Fatal(err)
	}

	if v := m.GetVerifier("route-1"); v == nil {
		t.Error("expected verifier for route-1")
	}
	if v := m.GetVerifier("route-2"); v != nil {
		t.Error("expected nil verifier for route-2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route-1" {
		t.Errorf("expected [route-1], got %v", ids)
	}
}

func TestMergeConfig(t *testing.T) {
	global := config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: "/global/ca.pem",
	}
	route := config.ClientMTLSConfig{
		ClientCAFile: "/route/ca.pem",
	}
	merged := MergeClientMTLSConfig(route, global)
	if !merged.Enabled {
		t.Error("expected merged.Enabled to be true")
	}
	if merged.ClientAuth != "verify" {
		t.Errorf("expected verify, got %s", merged.ClientAuth)
	}
	if merged.ClientCAFile != "/route/ca.pem" {
		t.Errorf("expected /route/ca.pem, got %s", merged.ClientCAFile)
	}
}

func TestNew_VerifyMode_NoCA_Error(t *testing.T) {
	_, err := New(config.ClientMTLSConfig{
		Enabled:    true,
		ClientAuth: "verify",
	})
	if err == nil {
		t.Error("expected error when verify mode has no CA files")
	}
}

func TestNew_InvalidCAFile_Error(t *testing.T) {
	_, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: "/nonexistent/ca.pem",
	})
	if err == nil {
		t.Error("expected error for nonexistent CA file")
	}
}

func TestNew_InvalidPEM_Error(t *testing.T) {
	dir := t.TempDir()
	badFile := filepath.Join(dir, "bad.pem")
	if err := os.WriteFile(badFile, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(config.ClientMTLSConfig{
		Enabled:      true,
		ClientAuth:   "verify",
		ClientCAFile: badFile,
	})
	if err == nil {
		t.Error("expected error for invalid PEM data")
	}
}
