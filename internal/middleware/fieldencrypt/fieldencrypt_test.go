package fieldencrypt

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/wudi/runway/config"
)

// testKeyBase64 is a valid 32-byte AES-256 key.
var testKeyBase64 = base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))

func TestNew_ValidConfig(t *testing.T) {
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		Algorithm:     "aes-gcm-256",
		KeyBase64:     testKeyBase64,
		EncryptFields: []string{"secret"},
	}

	fe, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fe == nil {
		t.Fatal("expected non-nil encryptor")
	}
}

func TestNew_InvalidKey(t *testing.T) {
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     "not-valid-base64!!!",
		EncryptFields: []string{"secret"},
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid base64 key")
	}
}

func TestNew_ShortKey(t *testing.T) {
	shortKey := base64.StdEncoding.EncodeToString([]byte("tooshort"))
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     shortKey,
		EncryptFields: []string{"secret"},
	}

	_, err := New(cfg)
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Errorf("expected key length error, got: %v", err)
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     testKeyBase64,
		EncryptFields: []string{"password"},
		DecryptFields: []string{"password"},
	}

	fe, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	original := `{"user":"alice","password":"secret123"}`

	// Encrypt
	encrypted, err := fe.encryptRequestFields([]byte(original))
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	// The password field should now be an encrypted string, not "secret123"
	encVal := gjson.GetBytes(encrypted, "password").String()
	if encVal == "secret123" {
		t.Error("password should have been encrypted")
	}

	// Decrypt
	decrypted, err := fe.decryptResponseFields(encrypted)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	// The password field should be restored to "secret123"
	decVal := gjson.GetBytes(decrypted, "password").String()
	if decVal != "secret123" {
		t.Errorf("expected 'secret123', got %q", decVal)
	}
}

func TestEncryptDecrypt_HexEncoding(t *testing.T) {
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     testKeyBase64,
		EncryptFields: []string{"token"},
		DecryptFields: []string{"token"},
		Encoding:      "hex",
	}

	fe, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	original := `{"token":"mytoken"}`
	encrypted, err := fe.encryptRequestFields([]byte(original))
	if err != nil {
		t.Fatal(err)
	}

	decrypted, err := fe.decryptResponseFields(encrypted)
	if err != nil {
		t.Fatal(err)
	}

	val := gjson.GetBytes(decrypted, "token").String()
	if val != "mytoken" {
		t.Errorf("expected 'mytoken', got %q", val)
	}
}

func TestMiddleware_EncryptsRequest(t *testing.T) {
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     testKeyBase64,
		EncryptFields: []string{"secret"},
	}

	fe, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	mw := fe.Middleware()
	var capturedBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"secret":"plaintext"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rr, r)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(capturedBody), &parsed); err != nil {
		t.Fatalf("failed to parse captured body: %v", err)
	}

	if parsed["secret"] == "plaintext" {
		t.Error("secret should have been encrypted")
	}
}

func TestMiddleware_DecryptsResponse(t *testing.T) {
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     testKeyBase64,
		EncryptFields: []string{"data"},
		DecryptFields: []string{"data"},
	}

	fe, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Pre-encrypt a value to put in the response
	encrypted, err := fe.encryptRequestFields([]byte(`{"data":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	encryptedBody := string(encrypted)

	mw := fe.Middleware()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(encryptedBody))
	})

	handler := mw(next)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, r)

	body := rr.Body.String()
	val := gjson.Get(body, "data").String()
	if val != "hello" {
		t.Errorf("expected decrypted 'hello', got %q", val)
	}
}

func TestEncrypt_MissingField(t *testing.T) {
	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     testKeyBase64,
		EncryptFields: []string{"missing_field"},
	}

	fe, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	input := `{"other":"value"}`
	result, err := fe.encryptRequestFields([]byte(input))
	if err != nil {
		t.Fatal(err)
	}

	// Should remain unchanged
	if string(result) != input {
		t.Errorf("expected unchanged body, got %s", string(result))
	}
}

func TestManager(t *testing.T) {
	m := NewFieldEncryptByRoute()

	cfg := config.FieldEncryptionConfig{
		Enabled:       true,
		KeyBase64:     testKeyBase64,
		EncryptFields: []string{"secret"},
	}

	if err := m.AddRoute("route1", cfg); err != nil {
		t.Fatal(err)
	}

	if fe := m.GetEncryptor("route1"); fe == nil {
		t.Error("expected encryptor for route1")
	}
	if fe := m.GetEncryptor("missing"); fe != nil {
		t.Error("expected nil for missing route")
	}

	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats")
	}
}
