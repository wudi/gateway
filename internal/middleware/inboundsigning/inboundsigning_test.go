package inboundsigning

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

var testSecret = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("A"), 32))

func signRequest(r *http.Request, secret []byte, prefix string, includeBody bool) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r.Header.Set(prefix+"Timestamp", ts)

	var bodyHash string
	if includeBody && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		h := sha256.Sum256(body)
		bodyHash = hex.EncodeToString(h[:])
	} else {
		h := sha256.Sum256(nil)
		bodyHash = hex.EncodeToString(h[:])
	}

	var sb strings.Builder
	sb.WriteString(r.Method)
	sb.WriteByte('\n')
	sb.WriteString(r.URL.RequestURI())
	sb.WriteByte('\n')
	sb.WriteString(ts)
	sb.WriteByte('\n')
	sb.WriteString(bodyHash)

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(sb.String()))
	sig := hex.EncodeToString(mac.Sum(nil))
	r.Header.Set(prefix+"Signature", "hmac-sha256="+sig)
}

func TestVerify_ValidSignature(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret,
	}

	v, err := New("test-route", cfg)
	if err != nil {
		t.Fatal(err)
	}

	secret, _ := base64.StdEncoding.DecodeString(testSecret)
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	signRequest(r, secret, "X-Gateway-", false)

	if err := v.Verify(r); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if v.metrics.Verified.Load() != 1 {
		t.Error("expected 1 verified")
	}
}

func TestVerify_ValidSignatureWithBody(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret,
	}

	v, err := New("test-route", cfg)
	if err != nil {
		t.Fatal(err)
	}

	secret, _ := base64.StdEncoding.DecodeString(testSecret)
	body := `{"hello":"world"}`
	r := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body))
	signRequest(r, secret, "X-Gateway-", true)

	if err := v.Verify(r); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}

	// Verify body is still readable after verification
	b, _ := io.ReadAll(r.Body)
	if string(b) != body {
		t.Errorf("body was not restored: got %q", string(b))
	}
}

func TestVerify_MissingTimestamp(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  testSecret,
	}

	v, _ := New("test-route", cfg)
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)

	err := v.Verify(r)
	if err == nil {
		t.Error("expected error for missing timestamp")
	}
	if v.metrics.Rejected.Load() != 1 {
		t.Error("expected 1 rejected")
	}
}

func TestVerify_ExpiredTimestamp(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  testSecret,
		MaxAge:  1 * time.Second,
	}

	v, _ := New("test-route", cfg)
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	r.Header.Set("X-Gateway-Timestamp", strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10))
	r.Header.Set("X-Gateway-Signature", "hmac-sha256=deadbeef")

	err := v.Verify(r)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected expiry error, got: %v", err)
	}
	if v.metrics.Expired.Load() != 1 {
		t.Error("expected 1 expired")
	}
}

func TestVerify_SignatureMismatch(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  testSecret,
	}

	v, _ := New("test-route", cfg)
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	r.Header.Set("X-Gateway-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	r.Header.Set("X-Gateway-Signature", "hmac-sha256="+strings.Repeat("ab", 32))

	err := v.Verify(r)
	if err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected mismatch error, got: %v", err)
	}
}

func TestVerify_KeyIDMismatch(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  testSecret,
		KeyID:   "expected-key",
	}

	v, _ := New("test-route", cfg)
	secret, _ := base64.StdEncoding.DecodeString(testSecret)
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	signRequest(r, secret, "X-Gateway-", false)
	r.Header.Set("X-Gateway-Key-ID", "wrong-key")

	err := v.Verify(r)
	if err == nil || !strings.Contains(err.Error(), "key ID mismatch") {
		t.Errorf("expected key ID mismatch, got: %v", err)
	}
}

func TestMiddleware_RejectsInvalidRequest(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  testSecret,
	}

	v, _ := New("test-route", cfg)
	mw := v.Middleware()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should not have been called")
	})
	handler := mw(next)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	handler.ServeHTTP(rr, r)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestMiddleware_ShadowModeAllowsThrough(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled:    true,
		Secret:     testSecret,
		ShadowMode: true,
	}

	v, _ := New("test-route", cfg)
	mw := v.Middleware()
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(next)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	handler.ServeHTTP(rr, r)

	if !called {
		t.Error("next handler should have been called in shadow mode")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 in shadow mode, got %d", rr.Code)
	}
}

func TestNew_InvalidSecret(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  "not-base64!!!",
	}
	_, err := New("test", cfg)
	if err == nil {
		t.Error("expected error for invalid base64 secret")
	}
}

func TestNew_ShortSecret(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  base64.StdEncoding.EncodeToString([]byte("short")),
	}
	_, err := New("test", cfg)
	if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Errorf("expected short secret error, got: %v", err)
	}
}

func TestMergeInboundSigningConfig(t *testing.T) {
	global := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret,
		MaxAge:    10 * time.Minute,
	}
	perRoute := config.InboundSigningConfig{
		Enabled:      true,
		HeaderPrefix: "X-Custom-",
	}

	merged := MergeInboundSigningConfig(perRoute, global)
	if merged.HeaderPrefix != "X-Custom-" {
		t.Errorf("expected per-route prefix, got %q", merged.HeaderPrefix)
	}
	if merged.Secret != testSecret {
		t.Error("expected global secret to be inherited")
	}
}

func TestManager(t *testing.T) {
	m := NewInboundSigningByRoute()

	cfg := config.InboundSigningConfig{
		Enabled: true,
		Secret:  testSecret,
	}

	if err := m.AddRoute("route1", cfg); err != nil {
		t.Fatal(err)
	}

	if v := m.GetVerifier("route1"); v == nil {
		t.Error("expected verifier for route1")
	}
	if v := m.GetVerifier("missing"); v != nil {
		t.Error("expected nil for missing route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats")
	}
}

// generateTestRSAKey generates a test RSA key pair.
func generateTestRSAKey(t *testing.T) (privKey *rsa.PrivateKey, privPEM, pubPEM string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}))
	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pubPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
	return key, privPEM, pubPEM
}

// rsaSignRequest signs a request using RSA-SHA256 (PKCS1v15) for testing.
func rsaSignRequest(t *testing.T, r *http.Request, key *rsa.PrivateKey, prefix string, includeBody bool) {
	t.Helper()
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r.Header.Set(prefix+"Timestamp", ts)

	var bodyHash string
	if includeBody && (r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		h := sha256.Sum256(body)
		bodyHash = hex.EncodeToString(h[:])
	} else {
		h := sha256.Sum256(nil)
		bodyHash = hex.EncodeToString(h[:])
	}

	var sb strings.Builder
	sb.WriteString(r.Method)
	sb.WriteByte('\n')
	sb.WriteString(r.URL.RequestURI())
	sb.WriteByte('\n')
	sb.WriteString(ts)
	sb.WriteByte('\n')
	sb.WriteString(bodyHash)

	h := sha256.New()
	h.Write([]byte(sb.String()))
	digest := h.Sum(nil)

	sigBytes, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(prefix+"Signature", "rsa-sha256="+hex.EncodeToString(sigBytes))
}

func TestVerify_RSA_SHA256_ValidSignature(t *testing.T) {
	privKey, _, pubPEM := generateTestRSAKey(t)

	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-sha256",
		PublicKey: pubPEM,
	}

	v, err := New("test-rsa", cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rsaSignRequest(t, r, privKey, "X-Gateway-", false)

	if err := v.Verify(r); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if v.metrics.Verified.Load() != 1 {
		t.Error("expected 1 verified")
	}
}

func TestVerify_RSA_SHA256_WithBody(t *testing.T) {
	privKey, _, pubPEM := generateTestRSAKey(t)

	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-sha256",
		PublicKey: pubPEM,
	}

	v, err := New("test-rsa-body", cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := `{"hello":"world"}`
	r := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body))
	rsaSignRequest(t, r, privKey, "X-Gateway-", true)

	if err := v.Verify(r); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}

	// Verify body is still readable
	b, _ := io.ReadAll(r.Body)
	if string(b) != body {
		t.Errorf("body was not restored: got %q", string(b))
	}
}

func TestVerify_RSA_WrongKey(t *testing.T) {
	privKey, _, _ := generateTestRSAKey(t)
	_, _, otherPubPEM := generateTestRSAKey(t) // different key pair

	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-sha256",
		PublicKey: otherPubPEM,
	}

	v, err := New("test-rsa-wrong", cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rsaSignRequest(t, r, privKey, "X-Gateway-", false)

	err = v.Verify(r)
	if err == nil {
		t.Error("expected error for wrong key")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("expected mismatch error, got: %v", err)
	}
	if v.metrics.Rejected.Load() != 1 {
		t.Error("expected 1 rejected")
	}
}

func TestVerify_RSA_PSS_SHA256(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))

	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-pss-sha256",
		PublicKey: pubPEM,
	}

	v, err := New("test-pss", cfg)
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	r.Header.Set("X-Gateway-Timestamp", ts)

	emptyHash := sha256.Sum256(nil)
	signingStr := "GET\n/api/test\n" + ts + "\n" + hex.EncodeToString(emptyHash[:])

	h := sha256.New()
	h.Write([]byte(signingStr))
	digest := h.Sum(nil)

	sigBytes, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Gateway-Signature", "rsa-pss-sha256="+hex.EncodeToString(sigBytes))

	if err := v.Verify(r); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestNew_RSA_InvalidPEM(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-sha256",
		PublicKey: "not-a-pem",
	}
	_, err := New("test", cfg)
	if err == nil {
		t.Error("expected error for invalid PEM")
	}
	if !strings.Contains(err.Error(), "PEM") {
		t.Errorf("expected PEM error, got: %v", err)
	}
}

func TestNew_RSA_MissingKey(t *testing.T) {
	cfg := config.InboundSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-sha256",
	}
	_, err := New("test", cfg)
	if err == nil {
		t.Error("expected error for missing RSA key")
	}
	if !strings.Contains(err.Error(), "public_key") {
		t.Errorf("expected public_key error, got: %v", err)
	}
}
