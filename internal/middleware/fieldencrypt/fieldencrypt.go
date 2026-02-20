package fieldencrypt

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
)

// FieldEncryptor performs AES-GCM encryption/decryption on specific JSON fields.
type FieldEncryptor struct {
	gcm           cipher.AEAD
	encryptFields []string
	decryptFields []string
	encode        func([]byte) string
	decode        func(string) ([]byte, error)
	total         atomic.Int64
	encrypted     atomic.Int64
	decrypted     atomic.Int64
	errors        atomic.Int64
}

// New creates a FieldEncryptor from config.
func New(cfg config.FieldEncryptionConfig) (*FieldEncryptor, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(cfg.KeyBase64)
	if err != nil {
		return nil, fmt.Errorf("field_encryption: invalid base64 key: %w", err)
	}
	if len(keyBytes) != 32 {
		return nil, fmt.Errorf("field_encryption: key must be exactly 32 bytes (got %d)", len(keyBytes))
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("field_encryption: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("field_encryption: %w", err)
	}

	encoding := cfg.Encoding
	if encoding == "" {
		encoding = "base64"
	}

	var encodeFn func([]byte) string
	var decodeFn func(string) ([]byte, error)
	switch encoding {
	case "base64":
		encodeFn = base64.StdEncoding.EncodeToString
		decodeFn = base64.StdEncoding.DecodeString
	case "hex":
		encodeFn = hex.EncodeToString
		decodeFn = hex.DecodeString
	default:
		return nil, fmt.Errorf("field_encryption: unsupported encoding %q", encoding)
	}

	return &FieldEncryptor{
		gcm:           gcm,
		encryptFields: cfg.EncryptFields,
		decryptFields: cfg.DecryptFields,
		encode:        encodeFn,
		decode:        decodeFn,
	}, nil
}

func (fe *FieldEncryptor) encryptValue(plaintext []byte) (string, error) {
	nonce := make([]byte, fe.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := fe.gcm.Seal(nonce, nonce, plaintext, nil)
	return fe.encode(ciphertext), nil
}

func (fe *FieldEncryptor) decryptValue(encoded string) ([]byte, error) {
	data, err := fe.decode(encoded)
	if err != nil {
		return nil, err
	}
	nonceSize := fe.gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return fe.gcm.Open(nil, nonce, ciphertext, nil)
}

func (fe *FieldEncryptor) encryptRequestFields(body []byte) ([]byte, error) {
	for _, path := range fe.encryptFields {
		result := gjson.GetBytes(body, path)
		if !result.Exists() {
			continue
		}
		var plaintext []byte
		if result.Type == gjson.String {
			plaintext = []byte(result.Str)
		} else {
			plaintext = []byte(result.Raw)
		}
		encrypted, err := fe.encryptValue(plaintext)
		if err != nil {
			return nil, fmt.Errorf("encrypt field %q: %w", path, err)
		}
		body, err = sjson.SetBytes(body, path, encrypted)
		if err != nil {
			return nil, fmt.Errorf("set field %q: %w", path, err)
		}
	}
	return body, nil
}

func (fe *FieldEncryptor) decryptResponseFields(body []byte) ([]byte, error) {
	for _, path := range fe.decryptFields {
		result := gjson.GetBytes(body, path)
		if !result.Exists() || result.Type != gjson.String {
			continue
		}
		decrypted, err := fe.decryptValue(result.Str)
		if err != nil {
			return nil, fmt.Errorf("decrypt field %q: %w", path, err)
		}
		// Try to detect if decrypted value is valid JSON
		if gjson.ValidBytes(decrypted) {
			body, err = sjson.SetRawBytes(body, path, decrypted)
		} else {
			body, err = sjson.SetBytes(body, path, string(decrypted))
		}
		if err != nil {
			return nil, fmt.Errorf("set field %q: %w", path, err)
		}
	}
	return body, nil
}

// Middleware returns a middleware that encrypts request fields and decrypts response fields.
func (fe *FieldEncryptor) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fe.total.Add(1)

			// Encrypt request fields
			if len(fe.encryptFields) > 0 && r.Body != nil {
				body, err := io.ReadAll(r.Body)
				if err == nil && len(body) > 0 && gjson.ValidBytes(body) {
					encrypted, err := fe.encryptRequestFields(body)
					if err != nil {
						fe.errors.Add(1)
					} else {
						fe.encrypted.Add(1)
						body = encrypted
					}
					r.Body = io.NopCloser(bytes.NewReader(body))
					r.ContentLength = int64(len(body))
				}
			}

			// For response decryption, wrap the response writer
			if len(fe.decryptFields) > 0 {
				bw := &bodyBufferWriter{
					ResponseWriter: w,
					fe:             fe,
				}
				next.ServeHTTP(bw, r)
				bw.flush()
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns metrics for this encryptor.
func (fe *FieldEncryptor) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total":          fe.total.Load(),
		"encrypted":      fe.encrypted.Load(),
		"decrypted":      fe.decrypted.Load(),
		"errors":         fe.errors.Load(),
		"encrypt_fields": len(fe.encryptFields),
		"decrypt_fields": len(fe.decryptFields),
	}
}

// bodyBufferWriter buffers the response body for field decryption.
type bodyBufferWriter struct {
	http.ResponseWriter
	fe          *FieldEncryptor
	buf         bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func (w *bodyBufferWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
}

func (w *bodyBufferWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.buf.Write(b)
}

func (w *bodyBufferWriter) flush() {
	body := w.buf.Bytes()

	if len(body) > 0 && gjson.ValidBytes(body) {
		decrypted, err := w.fe.decryptResponseFields(body)
		if err != nil {
			w.fe.errors.Add(1)
		} else {
			w.fe.decrypted.Add(1)
			body = decrypted
		}
	}

	w.ResponseWriter.Header().Del("Content-Length")
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.statusCode)
	w.ResponseWriter.Write(body)
}

// FieldEncryptByRoute manages per-route field encryptors.
type FieldEncryptByRoute struct {
	byroute.Manager[*FieldEncryptor]
}

// NewFieldEncryptByRoute creates a new manager.
func NewFieldEncryptByRoute() *FieldEncryptByRoute {
	return &FieldEncryptByRoute{}
}

// AddRoute adds a field encryptor for a route.
func (m *FieldEncryptByRoute) AddRoute(routeID string, cfg config.FieldEncryptionConfig) error {
	fe, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, fe)
	return nil
}

// GetEncryptor returns the field encryptor for a route.
func (m *FieldEncryptByRoute) GetEncryptor(routeID string) *FieldEncryptor {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route field encryption metrics.
func (m *FieldEncryptByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(fe *FieldEncryptor) interface{} { return fe.Stats() })
}
