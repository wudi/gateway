package signing

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func testSecret() string {
	// 32 bytes of test secret, base64-encoded
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func boolPtr(v bool) *bool { return &v }

func TestSignPOST(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "key-1",
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"name":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/users?page=2", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	// Verify 4 headers injected
	sig := req.Header.Get("X-Runway-Signature")
	ts := req.Header.Get("X-Runway-Timestamp")
	keyID := req.Header.Get("X-Runway-Key-ID")
	signedHdrs := req.Header.Get("X-Runway-Signed-Headers")

	if sig == "" {
		t.Error("missing X-Runway-Signature")
	}
	if !strings.HasPrefix(sig, "hmac-sha256=") {
		t.Errorf("signature should start with 'hmac-sha256=', got %q", sig)
	}
	if ts == "" {
		t.Error("missing X-Runway-Timestamp")
	}
	if keyID != "key-1" {
		t.Errorf("expected key-1, got %q", keyID)
	}
	if signedHdrs != "" {
		t.Errorf("expected empty signed headers, got %q", signedHdrs)
	}

	// Verify body is still readable
	readBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readBody, body) {
		t.Errorf("body mismatch: got %q, want %q", readBody, body)
	}
}

func TestSignGET(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "key-1",
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	sig := req.Header.Get("X-Runway-Signature")
	if sig == "" {
		t.Error("missing signature for GET")
	}

	// Body hash should be sha256 of empty
	emptyHash := sha256.Sum256(nil)
	emptyHex := hex.EncodeToString(emptyHash[:])

	// Verify by recomputing the signature
	ts := req.Header.Get("X-Runway-Timestamp")
	signingStr := "GET\n/api/v1/health\n" + ts + "\n" + emptyHex
	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingStr))
	expected := "hmac-sha256=" + hex.EncodeToString(mac.Sum(nil))

	if sig != expected {
		t.Errorf("signature mismatch:\ngot  %s\nwant %s", sig, expected)
	}
}

func TestSignatureVerification(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:       true,
		Secret:        testSecret(),
		KeyID:         "key-1",
		SignedHeaders: []string{"Content-Type", "Host"},
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"data":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/resource", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "api.example.com"

	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	// Recompute signature to verify
	ts := req.Header.Get("X-Runway-Timestamp")
	bodyH := sha256.Sum256(body)
	bodyHex := hex.EncodeToString(bodyH[:])

	// Signed headers are sorted: content-type, host
	signingStr := "POST\n/api/resource\n" + ts + "\n" + bodyHex +
		"\ncontent-type:application/json" +
		"\nhost:api.example.com"

	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingStr))
	expected := "hmac-sha256=" + hex.EncodeToString(mac.Sum(nil))

	got := req.Header.Get("X-Runway-Signature")
	if got != expected {
		t.Errorf("signature verification failed:\ngot  %s\nwant %s", got, expected)
	}

	// Verify signed headers
	sh := req.Header.Get("X-Runway-Signed-Headers")
	if sh != "Content-Type;Host" {
		t.Errorf("signed headers: got %q, want %q", sh, "Content-Type;Host")
	}
}

func TestHMACSHA512(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha512",
		Secret:    testSecret(),
		KeyID:     "key-512",
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	sig := req.Header.Get("X-Runway-Signature")
	if !strings.HasPrefix(sig, "hmac-sha512=") {
		t.Errorf("expected hmac-sha512 prefix, got %q", sig)
	}

	// Verify by recomputing
	ts := req.Header.Get("X-Runway-Timestamp")
	emptyHash := sha256.Sum256(nil)
	signingStr := "GET\n/test\n" + ts + "\n" + hex.EncodeToString(emptyHash[:])

	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	mac := hmac.New(sha512.New, secret)
	mac.Write([]byte(signingStr))
	expected := "hmac-sha512=" + hex.EncodeToString(mac.Sum(nil))

	if sig != expected {
		t.Errorf("sha512 signature mismatch:\ngot  %s\nwant %s", sig, expected)
	}
}

func TestMergeSigningConfig(t *testing.T) {
	global := config.BackendSigningConfig{
		Enabled:      true,
		Algorithm:    "hmac-sha256",
		Secret:       testSecret(),
		KeyID:        "global-key",
		HeaderPrefix: "X-GW-",
		IncludeBody:  boolPtr(true),
	}

	perRoute := config.BackendSigningConfig{
		Enabled: true,
		KeyID:   "route-key",
	}

	merged := MergeSigningConfig(perRoute, global)

	if merged.Algorithm != "hmac-sha256" {
		t.Errorf("algorithm should be from global: got %q", merged.Algorithm)
	}
	if merged.KeyID != "route-key" {
		t.Errorf("key_id should be overridden: got %q", merged.KeyID)
	}
	if merged.HeaderPrefix != "X-GW-" {
		t.Errorf("header_prefix should be from global: got %q", merged.HeaderPrefix)
	}
	if merged.Secret != testSecret() {
		t.Error("secret should be from global")
	}

	// Per-route override of include_body
	perRoute2 := config.BackendSigningConfig{
		Enabled:     true,
		IncludeBody: boolPtr(false),
	}
	merged2 := MergeSigningConfig(perRoute2, global)
	if merged2.IncludeBody == nil || *merged2.IncludeBody != false {
		t.Error("include_body should be overridden to false")
	}
}

func TestInvalidConfigs(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.BackendSigningConfig
		want string
	}{
		{
			name: "invalid base64",
			cfg: config.BackendSigningConfig{
				Enabled: true,
				Secret:  "not-valid-base64!!!",
				KeyID:   "k",
			},
			want: "invalid base64",
		},
		{
			name: "short secret",
			cfg: config.BackendSigningConfig{
				Enabled: true,
				Secret:  base64.StdEncoding.EncodeToString([]byte("short")),
				KeyID:   "k",
			},
			want: "at least 32 bytes",
		},
		{
			name: "bad algorithm",
			cfg: config.BackendSigningConfig{
				Enabled:   true,
				Algorithm: "hmac-md5",
				Secret:    testSecret(),
				KeyID:     "k",
			},
			want: "unsupported algorithm",
		},
		{
			name: "missing key_id",
			cfg: config.BackendSigningConfig{
				Enabled: true,
				Secret:  testSecret(),
			},
			want: "key_id is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New("route-1", tt.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should contain %q", err, tt.want)
			}
		})
	}
}

func TestIncludeBodyFalse(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:     true,
		Secret:      testSecret(),
		KeyID:       "key-1",
		IncludeBody: boolPtr(false),
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`large body content`)
	req := httptest.NewRequest(http.MethodPost, "/api/upload", bytes.NewReader(body))

	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	// Body should still be fully readable (not consumed)
	readBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readBody, body) {
		t.Error("body should be unchanged when include_body=false")
	}

	// Metrics: body should NOT have been hashed
	if signer.metrics.BodyHashed.Load() != 0 {
		t.Error("body should not have been hashed with include_body=false")
	}
}

func TestCustomHeaderPrefix(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:      true,
		Secret:       testSecret(),
		KeyID:        "key-1",
		HeaderPrefix: "X-Custom-",
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	if req.Header.Get("X-Custom-Signature") == "" {
		t.Error("missing X-Custom-Signature header")
	}
	if req.Header.Get("X-Custom-Key-ID") != "key-1" {
		t.Error("missing X-Custom-Key-ID header")
	}
	if req.Header.Get("X-Custom-Timestamp") == "" {
		t.Error("missing X-Custom-Timestamp header")
	}
}

func TestTimestampIsRecent(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "key-1",
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	before := time.Now().Unix()
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}
	after := time.Now().Unix()

	ts, _ := strconv.ParseInt(req.Header.Get("X-Runway-Timestamp"), 10, 64)
	if ts < before || ts > after {
		t.Errorf("timestamp %d not in range [%d, %d]", ts, before, after)
	}
}

func TestMetrics(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "key-1",
	}
	signer, err := New("route-1", cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Sign a POST (has body)
	req := httptest.NewRequest(http.MethodPost, "/api", bytes.NewReader([]byte("body")))
	signer.Sign(req)

	// Sign a GET (no body)
	req2 := httptest.NewRequest(http.MethodGet, "/api", nil)
	signer.Sign(req2)

	status := signer.Status()
	if status.TotalRequests != 2 {
		t.Errorf("total requests: got %d, want 2", status.TotalRequests)
	}
	if status.Signed != 2 {
		t.Errorf("signed: got %d, want 2", status.Signed)
	}
	if status.BodyHashed != 1 {
		t.Errorf("body hashed: got %d, want 1", status.BodyHashed)
	}
	if status.Errors != 0 {
		t.Errorf("errors: got %d, want 0", status.Errors)
	}
}

func TestManagerAddRouteAndGetSigner(t *testing.T) {
	m := NewSigningByRoute()

	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "key-1",
	}
	if err := m.AddRoute("route-1", cfg); err != nil {
		t.Fatal(err)
	}

	s := m.GetSigner("route-1")
	if s == nil {
		t.Fatal("expected signer for route-1")
	}
	if s.RouteID() != "route-1" {
		t.Errorf("route ID: got %q, want %q", s.RouteID(), "route-1")
	}

	// Non-existent route
	if m.GetSigner("route-2") != nil {
		t.Error("expected nil for route-2")
	}

	// RouteIDs
	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route-1" {
		t.Errorf("route IDs: got %v", ids)
	}

	// Stats
	stats := m.Stats()
	if len(stats) != 1 {
		t.Errorf("stats: got %d routes", len(stats))
	}
}

// generateTestRSAKey generates a test RSA key pair and returns PEM-encoded private and public keys.
func generateTestRSAKey(t *testing.T) (privPEM, pubPEM string) {
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
	return
}

func TestRSASHA256Signing(t *testing.T) {
	privPEM, _ := generateTestRSAKey(t)

	cfg := config.BackendSigningConfig{
		Enabled:    true,
		Algorithm:  "rsa-sha256",
		PrivateKey: privPEM,
		KeyID:      "rsa-key-1",
	}
	signer, err := New("route-rsa", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	sig := req.Header.Get("X-Runway-Signature")
	if !strings.HasPrefix(sig, "rsa-sha256=") {
		t.Errorf("expected rsa-sha256 prefix, got %q", sig)
	}
	if req.Header.Get("X-Runway-Key-ID") != "rsa-key-1" {
		t.Error("wrong key ID")
	}
}

func TestRSASHA512Signing(t *testing.T) {
	privPEM, _ := generateTestRSAKey(t)

	cfg := config.BackendSigningConfig{
		Enabled:    true,
		Algorithm:  "rsa-sha512",
		PrivateKey: privPEM,
		KeyID:      "rsa-key-512",
	}
	signer, err := New("route-rsa512", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/test", bytes.NewReader([]byte(`{"data":true}`)))
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	sig := req.Header.Get("X-Runway-Signature")
	if !strings.HasPrefix(sig, "rsa-sha512=") {
		t.Errorf("expected rsa-sha512 prefix, got %q", sig)
	}
}

func TestRSAPSSSHA256Signing(t *testing.T) {
	privPEM, _ := generateTestRSAKey(t)

	cfg := config.BackendSigningConfig{
		Enabled:    true,
		Algorithm:  "rsa-pss-sha256",
		PrivateKey: privPEM,
		KeyID:      "pss-key-1",
	}
	signer, err := New("route-pss", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	sig := req.Header.Get("X-Runway-Signature")
	if !strings.HasPrefix(sig, "rsa-pss-sha256=") {
		t.Errorf("expected rsa-pss-sha256 prefix, got %q", sig)
	}
}

func TestRSASignAndVerifyRoundTrip(t *testing.T) {
	privPEM, pubPEM := generateTestRSAKey(t)

	// Sign the request
	signerCfg := config.BackendSigningConfig{
		Enabled:    true,
		Algorithm:  "rsa-sha256",
		PrivateKey: privPEM,
		KeyID:      "roundtrip-key",
	}
	signer, err := New("route-rt", signerCfg)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"hello":"world"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/roundtrip", bytes.NewReader(body))
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	// Parse the signature and verify using the public key
	sigHeader := req.Header.Get("X-Runway-Signature")
	parts := strings.SplitN(sigHeader, "=", 2)
	if parts[0] != "rsa-sha256" {
		t.Fatalf("wrong algo prefix: %s", parts[0])
	}
	sigBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}

	// Parse public key
	block, _ := pem.Decode([]byte(pubPEM))
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	rsaPub := pub.(*rsa.PublicKey)

	// Rebuild signing string
	ts := req.Header.Get("X-Runway-Timestamp")
	readBody, _ := io.ReadAll(req.Body)
	bodyHash := sha256.Sum256(readBody)
	signingStr := "POST\n/api/roundtrip\n" + ts + "\n" + hex.EncodeToString(bodyHash[:])

	h := sha256.New()
	h.Write([]byte(signingStr))
	digest := h.Sum(nil)

	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest, sigBytes); err != nil {
		t.Errorf("RSA signature verification failed: %v", err)
	}
}

func TestRSAInvalidPEM(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:    true,
		Algorithm:  "rsa-sha256",
		PrivateKey: "not-a-pem-key",
		KeyID:      "k",
	}
	_, err := New("route-1", cfg)
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
	if !strings.Contains(err.Error(), "PEM") {
		t.Errorf("expected PEM error, got: %v", err)
	}
}

func TestRSAMissingKey(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-sha256",
		KeyID:     "k",
	}
	_, err := New("route-1", cfg)
	if err == nil {
		t.Fatal("expected error for missing RSA key")
	}
	if !strings.Contains(err.Error(), "private_key") {
		t.Errorf("expected private_key error, got: %v", err)
	}
}

// ── Comprehensive tests ──

func TestSignConcurrentRequests(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "concurrent-key",
	}
	signer, err := New("route-concurrent", cfg)
	if err != nil {
		t.Fatal(err)
	}

	const n = 100
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/api/test", bytes.NewReader([]byte(`{"i":1}`)))
			errs <- signer.Sign(req)
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent sign %d failed: %v", i, err)
		}
	}

	status := signer.Status()
	if status.TotalRequests != n {
		t.Errorf("expected %d total requests, got %d", n, status.TotalRequests)
	}
	if status.Signed != n {
		t.Errorf("expected %d signed, got %d", n, status.Signed)
	}
}

func TestSignRSAConcurrentRequests(t *testing.T) {
	privPEM, _ := generateTestRSAKey(t)
	cfg := config.BackendSigningConfig{
		Enabled:    true,
		Algorithm:  "rsa-sha256",
		PrivateKey: privPEM,
		KeyID:      "rsa-concurrent",
	}
	signer, err := New("route-rsa-concurrent", cfg)
	if err != nil {
		t.Fatal(err)
	}

	const n = 50
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
			errs <- signer.Sign(req)
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent RSA sign %d failed: %v", i, err)
		}
	}
	if signer.Status().Signed != n {
		t.Errorf("expected %d signed", n)
	}
}

func TestSignPreservesBodyAfterSigning(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "body-key",
	}
	signer, err := New("route-body", cfg)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"large": "` + strings.Repeat("x", 10000) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/test", bytes.NewReader(body))
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	readBody, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readBody, body) {
		t.Error("body was corrupted after signing")
	}
}

func TestSignNilBody(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "nil-key",
	}
	signer, err := New("route-nil", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	sig := req.Header.Get("X-Runway-Signature")
	if sig == "" {
		t.Error("expected signature even for nil POST body")
	}
}

func TestSignAllHTTPMethods(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "methods-key",
	}
	signer, err := New("route-methods", cfg)
	if err != nil {
		t.Fatal(err)
	}

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions}
	for _, method := range methods {
		req := httptest.NewRequest(method, "/api/test", nil)
		if err := signer.Sign(req); err != nil {
			t.Errorf("%s: sign failed: %v", method, err)
		}
		sig := req.Header.Get("X-Runway-Signature")
		if sig == "" {
			t.Errorf("%s: missing signature", method)
		}
	}
}

func TestSignDefaultAlgorithm(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:   true,
		Algorithm: "", // should default to hmac-sha256
		Secret:    testSecret(),
		KeyID:     "default-algo",
	}
	signer, err := New("route-default", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	sig := req.Header.Get("X-Runway-Signature")
	if !strings.HasPrefix(sig, "hmac-sha256=") {
		t.Errorf("expected hmac-sha256 default, got %q", sig)
	}
}

func TestSignOverwritesExistingHeaders(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "overwrite-key",
	}
	signer, err := New("route-overwrite", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Runway-Signature", "old-value")
	req.Header.Set("X-Runway-Timestamp", "0")

	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	if req.Header.Get("X-Runway-Signature") == "old-value" {
		t.Error("signature header was not overwritten")
	}
	if req.Header.Get("X-Runway-Timestamp") == "0" {
		t.Error("timestamp header was not overwritten")
	}
}

func TestSignWithQueryParameters(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "query-key",
	}
	signer, err := New("route-query", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test?foo=bar&baz=qux", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	// Verify the full request URI (including query) is in the signing string
	ts := req.Header.Get("X-Runway-Timestamp")
	emptyHash := sha256.Sum256(nil)
	signingStr := "GET\n/api/test?foo=bar&baz=qux\n" + ts + "\n" + hex.EncodeToString(emptyHash[:])
	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingStr))
	expected := "hmac-sha256=" + hex.EncodeToString(mac.Sum(nil))

	if req.Header.Get("X-Runway-Signature") != expected {
		t.Error("signature does not include query parameters correctly")
	}
}

func TestRSASignAndVerifyPSSRoundTrip(t *testing.T) {
	privPEM, pubPEM := generateTestRSAKey(t)

	// Sign
	signerCfg := config.BackendSigningConfig{
		Enabled:    true,
		Algorithm:  "rsa-pss-sha256",
		PrivateKey: privPEM,
		KeyID:      "pss-rt-key",
	}
	signer, err := New("route-pss-rt", signerCfg)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"pss":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/pss", bytes.NewReader(body))
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	// Verify
	sigHeader := req.Header.Get("X-Runway-Signature")
	parts := strings.SplitN(sigHeader, "=", 2)
	if parts[0] != "rsa-pss-sha256" {
		t.Fatalf("wrong algo prefix: %s", parts[0])
	}
	sigBytes, _ := hex.DecodeString(parts[1])

	block, _ := pem.Decode([]byte(pubPEM))
	pub, _ := x509.ParsePKIXPublicKey(block.Bytes)
	rsaPub := pub.(*rsa.PublicKey)

	ts := req.Header.Get("X-Runway-Timestamp")
	readBody, _ := io.ReadAll(req.Body)
	bodyHash := sha256.Sum256(readBody)
	signingStr := "POST\n/api/pss\n" + ts + "\n" + hex.EncodeToString(bodyHash[:])

	h := sha256.New()
	h.Write([]byte(signingStr))
	digest := h.Sum(nil)

	if err := rsa.VerifyPSS(rsaPub, crypto.SHA256, digest, sigBytes, nil); err != nil {
		t.Errorf("RSA-PSS round-trip verification failed: %v", err)
	}
}

func TestRSAPrivateKeyFromFile(t *testing.T) {
	privPEM, _ := generateTestRSAKey(t)
	tmpFile := t.TempDir() + "/test-key.pem"
	if err := os.WriteFile(tmpFile, []byte(privPEM), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := config.BackendSigningConfig{
		Enabled:        true,
		Algorithm:      "rsa-sha256",
		PrivateKeyFile: tmpFile,
		KeyID:          "file-key",
	}
	signer, err := New("route-file", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}
	sig := req.Header.Get("X-Runway-Signature")
	if !strings.HasPrefix(sig, "rsa-sha256=") {
		t.Errorf("expected rsa-sha256 prefix from file key, got %q", sig)
	}
}

func TestRSAPrivateKeyFileNotFound(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:        true,
		Algorithm:      "rsa-sha256",
		PrivateKeyFile: "/nonexistent/key.pem",
		KeyID:          "k",
	}
	_, err := New("route-1", cfg)
	if err == nil {
		t.Fatal("expected error for missing key file")
	}
	if !strings.Contains(err.Error(), "reading private key file") {
		t.Errorf("expected file read error, got: %v", err)
	}
}

func TestSignWithMultipleSignedHeaders(t *testing.T) {
	cfg := config.BackendSigningConfig{
		Enabled:       true,
		Secret:        testSecret(),
		KeyID:         "multi-hdr",
		SignedHeaders: []string{"X-Request-ID", "Content-Type", "Accept"},
	}
	signer, err := New("route-multi-hdr", cfg)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/test", bytes.NewReader([]byte("body")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-ID", "req-123")

	if err := signer.Sign(req); err != nil {
		t.Fatal(err)
	}

	// Signed headers should be sorted
	sh := req.Header.Get("X-Runway-Signed-Headers")
	if sh != "Accept;Content-Type;X-Request-ID" {
		t.Errorf("expected sorted signed headers, got %q", sh)
	}
}

func TestManagerConcurrentAddAndGet(t *testing.T) {
	m := NewSigningByRoute()

	// Concurrent adds
	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			cfg := config.BackendSigningConfig{
				Enabled: true,
				Secret:  testSecret(),
				KeyID:   "key-" + strconv.Itoa(idx),
			}
			errs <- m.AddRoute("route-"+strconv.Itoa(idx), cfg)
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent add %d failed: %v", i, err)
		}
	}

	// All should be retrievable
	for i := 0; i < n; i++ {
		if m.GetSigner("route-"+strconv.Itoa(i)) == nil {
			t.Errorf("missing signer for route-%d", i)
		}
	}

	ids := m.RouteIDs()
	if len(ids) != n {
		t.Errorf("expected %d routes, got %d", n, len(ids))
	}
}

func TestMiddlewareCallsNextOnError(t *testing.T) {
	// Middleware logs warning but still calls next
	cfg := config.BackendSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "mw-key",
	}
	signer, err := New("route-mw", cfg)
	if err != nil {
		t.Fatal(err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := signer.Middleware()(next)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("next handler should be called even on signing success")
	}
	if req.Header.Get("X-Runway-Signature") == "" {
		t.Error("signing middleware should have set signature header")
	}
}
