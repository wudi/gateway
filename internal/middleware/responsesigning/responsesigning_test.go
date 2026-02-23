package responsesigning

import (
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func testSecret() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestHMACSHA256Signing(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "key-1",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	responseBody := "hello world"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseBody))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	// Verify signature header is present
	sigHeader := rr.Header().Get("X-Response-Signature")
	if sigHeader == "" {
		t.Fatal("missing X-Response-Signature header")
	}

	// Verify format: keyId=...,algorithm=...,signature=...
	if !strings.HasPrefix(sigHeader, "keyId=key-1,algorithm=hmac-sha256,signature=") {
		t.Errorf("unexpected signature format: %q", sigHeader)
	}

	// Extract and verify the actual signature
	parts := strings.SplitN(sigHeader, "signature=", 2)
	if len(parts) != 2 {
		t.Fatal("could not extract signature value")
	}
	sigB64 := parts[1]
	sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		t.Fatalf("invalid base64 signature: %v", err)
	}

	// Recompute: keyID + "\n" + "\n" + body (no include_headers)
	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	signingContent := "key-1\n\nhello world"
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingContent))
	expected := mac.Sum(nil)

	if !hmac.Equal(sigBytes, expected) {
		t.Errorf("HMAC-SHA256 signature mismatch")
	}

	// Verify response body is preserved
	body := rr.Body.String()
	if body != responseBody {
		t.Errorf("body mismatch: got %q, want %q", body, responseBody)
	}

	// Verify status code is preserved
	if rr.Code != http.StatusOK {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestHMACSHA512Signing(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha512",
		Secret:    testSecret(),
		KeyID:     "key-512",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	responseBody := "sha512 body"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(responseBody))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	sigHeader := rr.Header().Get("X-Response-Signature")
	if !strings.Contains(sigHeader, "algorithm=hmac-sha512") {
		t.Errorf("expected hmac-sha512 algorithm in header, got %q", sigHeader)
	}

	// Verify the signature
	parts := strings.SplitN(sigHeader, "signature=", 2)
	if len(parts) != 2 {
		t.Fatal("could not extract signature value")
	}
	sigBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("invalid base64 signature: %v", err)
	}

	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	signingContent := "key-512\n\nsha512 body"
	mac := hmac.New(sha512.New, secret)
	mac.Write([]byte(signingContent))
	expected := mac.Sum(nil)

	if !hmac.Equal(sigBytes, expected) {
		t.Error("HMAC-SHA512 signature mismatch")
	}

	// Verify status code preserved
	if rr.Code != http.StatusCreated {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusCreated)
	}
}

func TestResponseBodyPreserved(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "body-key",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	largeBody := strings.Repeat("x", 10000)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Custom", "value")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeBody))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Body.String() != largeBody {
		t.Errorf("large body not preserved: got length %d, want %d", rr.Body.Len(), len(largeBody))
	}

	// Verify other headers are preserved
	if rr.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("Content-Type not preserved: got %q", rr.Header().Get("Content-Type"))
	}
	if rr.Header().Get("X-Custom") != "value" {
		t.Errorf("X-Custom not preserved: got %q", rr.Header().Get("X-Custom"))
	}

	// Signature header should be present
	if rr.Header().Get("X-Response-Signature") == "" {
		t.Error("missing signature header")
	}
}

func TestStatsTracking(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "stats-key",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := signer.Middleware()(inner)

	// Make 3 requests
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}

	if signer.TotalSigned() != 3 {
		t.Errorf("total signed: got %d, want 3", signer.TotalSigned())
	}
	if signer.Errors() != 0 {
		t.Errorf("errors: got %d, want 0", signer.Errors())
	}
	if signer.Algorithm() != "hmac-sha256" {
		t.Errorf("algorithm: got %q, want %q", signer.Algorithm(), "hmac-sha256")
	}
}

func TestByRouteManager(t *testing.T) {
	m := NewSignerByRoute()

	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "route-key",
	}
	if err := m.AddRoute("route-1", cfg); err != nil {
		t.Fatal(err)
	}

	s := m.GetSigner("route-1")
	if s == nil {
		t.Fatal("expected signer for route-1")
	}
	if s.Algorithm() != "hmac-sha256" {
		t.Errorf("algorithm: got %q, want %q", s.Algorithm(), "hmac-sha256")
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
		t.Errorf("stats: got %d routes, want 1", len(stats))
	}
	routeStats, ok := stats["route-1"].(map[string]interface{})
	if !ok {
		t.Fatal("route-1 stats not a map")
	}
	if routeStats["algorithm"] != "hmac-sha256" {
		t.Errorf("stats algorithm: got %v", routeStats["algorithm"])
	}
}

func TestByRouteAddError(t *testing.T) {
	m := NewSignerByRoute()

	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    "not-valid-base64!!!",
		KeyID:     "k",
	}
	if err := m.AddRoute("route-1", cfg); err == nil {
		t.Fatal("expected error for invalid secret")
	}

	if m.GetSigner("route-1") != nil {
		t.Error("signer should not be stored after error")
	}
}

func TestIncludeHeaders(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:        true,
		Algorithm:      "hmac-sha256",
		Secret:         testSecret(),
		KeyID:          "hdr-key",
		IncludeHeaders: []string{"X-Request-ID", "Content-Type"},
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "req-123")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("body"))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	sigHeader := rr.Header().Get("X-Response-Signature")
	if sigHeader == "" {
		t.Fatal("missing signature header")
	}

	// Verify by recomputing: include_headers are sorted, so Content-Type comes before X-Request-ID
	parts := strings.SplitN(sigHeader, "signature=", 2)
	sigBytes, _ := base64.StdEncoding.DecodeString(parts[1])

	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	// Sorted: Content-Type, X-Request-ID
	signingContent := "hdr-key\ncontent-type:application/json\nx-request-id:req-123\nbody"
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingContent))
	expected := mac.Sum(nil)

	if !hmac.Equal(sigBytes, expected) {
		t.Error("signature with include_headers mismatch")
	}
}

func TestCustomHeaderName(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "custom-hdr",
		Header:    "X-My-Signature",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Header().Get("X-My-Signature") == "" {
		t.Error("missing custom signature header X-My-Signature")
	}
	if rr.Header().Get("X-Response-Signature") != "" {
		t.Error("default header should not be set when custom header is configured")
	}
}

func TestDefaultAlgorithm(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled: true,
		Secret:  testSecret(),
		KeyID:   "default-algo",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	sigHeader := rr.Header().Get("X-Response-Signature")
	if !strings.Contains(sigHeader, "algorithm=hmac-sha256") {
		t.Errorf("expected default hmac-sha256 algorithm, got %q", sigHeader)
	}
}

func TestInvalidConfigs(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.ResponseSigningConfig
		want string
	}{
		{
			name: "not enabled",
			cfg:  config.ResponseSigningConfig{Enabled: false},
			want: "not enabled",
		},
		{
			name: "invalid base64 secret",
			cfg: config.ResponseSigningConfig{
				Enabled:   true,
				Algorithm: "hmac-sha256",
				Secret:    "not-valid-base64!!!",
				KeyID:     "k",
			},
			want: "invalid base64",
		},
		{
			name: "short secret",
			cfg: config.ResponseSigningConfig{
				Enabled:   true,
				Algorithm: "hmac-sha256",
				Secret:    base64.StdEncoding.EncodeToString([]byte("short")),
				KeyID:     "k",
			},
			want: "at least 32 bytes",
		},
		{
			name: "unsupported algorithm",
			cfg: config.ResponseSigningConfig{
				Enabled:   true,
				Algorithm: "hmac-md5",
				Secret:    testSecret(),
				KeyID:     "k",
			},
			want: "unsupported algorithm",
		},
		{
			name: "missing HMAC secret",
			cfg: config.ResponseSigningConfig{
				Enabled:   true,
				Algorithm: "hmac-sha256",
				KeyID:     "k",
			},
			want: "secret is required",
		},
		{
			name: "RSA missing key file",
			cfg: config.ResponseSigningConfig{
				Enabled:   true,
				Algorithm: "rsa-sha256",
				KeyID:     "k",
			},
			want: "key_file is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q should contain %q", err, tt.want)
			}
		})
	}
}

func TestRSASHA256Signing(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(privKey)
	if err != nil {
		t.Fatal(err)
	}
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})

	tmpFile := t.TempDir() + "/test-key.pem"
	if err := os.WriteFile(tmpFile, privPEM, 0600); err != nil {
		t.Fatal(err)
	}

	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "rsa-sha256",
		KeyFile:   tmpFile,
		KeyID:     "rsa-key-1",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	responseBody := "rsa signed body"
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseBody))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	sigHeader := rr.Header().Get("X-Response-Signature")
	if !strings.Contains(sigHeader, "algorithm=rsa-sha256") {
		t.Errorf("expected rsa-sha256 algorithm, got %q", sigHeader)
	}
	if !strings.Contains(sigHeader, "keyId=rsa-key-1") {
		t.Errorf("expected keyId=rsa-key-1, got %q", sigHeader)
	}

	// Verify the RSA signature
	parts := strings.SplitN(sigHeader, "signature=", 2)
	sigBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("invalid base64 signature: %v", err)
	}

	signingContent := "rsa-key-1\n\nrsa signed body"
	h := sha256.New()
	h.Write([]byte(signingContent))
	digest := h.Sum(nil)

	if err := rsa.VerifyPKCS1v15(&privKey.PublicKey, crypto.SHA256, digest, sigBytes); err != nil {
		t.Errorf("RSA signature verification failed: %v", err)
	}

	// Verify body is preserved
	if rr.Body.String() != responseBody {
		t.Errorf("body: got %q, want %q", rr.Body.String(), responseBody)
	}

	// Verify stats
	if signer.TotalSigned() != 1 {
		t.Errorf("total signed: got %d, want 1", signer.TotalSigned())
	}
}

func TestEmptyBody(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "empty-key",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodDelete, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	sigHeader := rr.Header().Get("X-Response-Signature")
	if sigHeader == "" {
		t.Fatal("missing signature header for empty body")
	}

	// Verify signature for empty body
	parts := strings.SplitN(sigHeader, "signature=", 2)
	sigBytes, _ := base64.StdEncoding.DecodeString(parts[1])

	secret, _ := base64.StdEncoding.DecodeString(testSecret())
	signingContent := "empty-key\n\n"
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signingContent))
	expected := mac.Sum(nil)

	if !hmac.Equal(sigBytes, expected) {
		t.Error("signature mismatch for empty body")
	}

	if rr.Code != http.StatusNoContent {
		t.Errorf("status code: got %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestStatusCodePreserved(t *testing.T) {
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "status-key",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	codes := []int{200, 201, 204, 301, 400, 404, 500}
	for _, code := range codes {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			if code != 204 {
				w.Write([]byte("body"))
			}
		})

		handler := signer.Middleware()(inner)
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)

		if rr.Code != code {
			t.Errorf("status %d: got %d", code, rr.Code)
		}
		if rr.Header().Get("X-Response-Signature") == "" {
			t.Errorf("status %d: missing signature", code)
		}
	}
}

func TestFlush(t *testing.T) {
	// Verify that the bufferingWriter's Flush via the Flusher interface is not needed
	// since we explicitly flush at the end of Middleware()
	cfg := config.ResponseSigningConfig{
		Enabled:   true,
		Algorithm: "hmac-sha256",
		Secret:    testSecret(),
		KeyID:     "flush-key",
	}
	signer, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("part1"))
		w.Write([]byte("part2"))
	})

	handler := signer.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	body, _ := io.ReadAll(rr.Body)
	if string(body) != "part1part2" {
		t.Errorf("multi-write body: got %q, want %q", body, "part1part2")
	}

	if rr.Header().Get("X-Response-Signature") == "" {
		t.Error("missing signature for multi-write body")
	}
}
