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
	"fmt"
	"hash"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/middleware/bufutil"
)

// signMode distinguishes HMAC from RSA signing modes.
type signMode int

const (
	signModeHMAC signMode = iota
	signModeRSA
)

// Signer signs HTTP response bodies and headers.
type Signer struct {
	algorithm      string
	secret         []byte          // HMAC secret (nil for RSA)
	rsaKey         *rsa.PrivateKey // RSA private key (nil for HMAC)
	hashFunc       func() hash.Hash
	cryptoHash     crypto.Hash
	mode           signMode
	keyID          string
	header         string
	includeHeaders []string
	totalSigned    atomic.Int64
	errors         atomic.Int64
}

// New creates a Signer from a ResponseSigningConfig.
func New(cfg config.ResponseSigningConfig) (*Signer, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("response signing not enabled")
	}

	algo := cfg.Algorithm
	if algo == "" {
		algo = "hmac-sha256"
	}

	s := &Signer{
		algorithm: algo,
		keyID:     cfg.KeyID,
	}

	switch algo {
	case "hmac-sha256":
		s.mode = signModeHMAC
		s.hashFunc = sha256.New
	case "hmac-sha512":
		s.mode = signModeHMAC
		s.hashFunc = sha512.New
	case "rsa-sha256":
		s.mode = signModeRSA
		s.hashFunc = sha256.New
		s.cryptoHash = crypto.SHA256
	default:
		return nil, fmt.Errorf("response signing: unsupported algorithm %q", algo)
	}

	// Load key material
	switch s.mode {
	case signModeHMAC:
		if cfg.Secret == "" {
			return nil, fmt.Errorf("response signing: secret is required for HMAC algorithms")
		}
		secret, err := base64.StdEncoding.DecodeString(cfg.Secret)
		if err != nil {
			return nil, fmt.Errorf("response signing: invalid base64 secret: %w", err)
		}
		if len(secret) < 32 {
			return nil, fmt.Errorf("response signing: secret must be at least 32 bytes (got %d)", len(secret))
		}
		s.secret = secret
	case signModeRSA:
		privKey, err := loadPrivateKey(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("response signing: %w", err)
		}
		s.rsaKey = privKey
	}

	// Set header name
	s.header = cfg.Header
	if s.header == "" {
		s.header = "X-Response-Signature"
	}

	// Sort include headers for deterministic signing
	headers := make([]string, len(cfg.IncludeHeaders))
	copy(headers, cfg.IncludeHeaders)
	sort.Strings(headers)
	s.includeHeaders = headers

	return s, nil
}

// loadPrivateKey loads an RSA private key from a PEM file.
func loadPrivateKey(file string) (*rsa.PrivateKey, error) {
	if file == "" {
		return nil, fmt.Errorf("key_file is required for RSA algorithms")
	}
	pemData, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block from key file")
	}

	// Try PKCS8 first, then PKCS1
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		rsaKey, err2 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err2 != nil {
			return nil, fmt.Errorf("parsing private key: not PKCS8 (%v) or PKCS1 (%v)", err, err2)
		}
		return rsaKey, nil
	}
	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private key is not RSA (got %T)", key)
	}
	return rsaKey, nil
}

// sign builds the signature content and computes the signature.
func (s *Signer) sign(headerValues map[string]string, body []byte) (string, error) {
	// Build signing content: keyID + "\n" + selected_header_values + "\n" + body
	var sb strings.Builder
	sb.WriteString(s.keyID)
	sb.WriteByte('\n')

	for i, hdr := range s.includeHeaders {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(strings.ToLower(hdr))
		sb.WriteByte(':')
		sb.WriteString(headerValues[hdr])
	}

	sb.WriteByte('\n')
	sb.Write(body)

	signingContent := []byte(sb.String())

	var sigBytes []byte
	switch s.mode {
	case signModeHMAC:
		mac := hmac.New(s.hashFunc, s.secret)
		mac.Write(signingContent)
		sigBytes = mac.Sum(nil)
	case signModeRSA:
		h := s.hashFunc()
		h.Write(signingContent)
		digest := h.Sum(nil)
		var err error
		sigBytes, err = rsa.SignPKCS1v15(rand.Reader, s.rsaKey, s.cryptoHash, digest)
		if err != nil {
			return "", fmt.Errorf("RSA sign failed: %w", err)
		}
	}

	sig := base64.StdEncoding.EncodeToString(sigBytes)
	return fmt.Sprintf("keyId=%s,algorithm=%s,signature=%s", s.keyID, s.algorithm, sig), nil
}

// Middleware returns a middleware that signs response bodies.
func (s *Signer) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := bufutil.New()
			next.ServeHTTP(bw, r)

			// Collect header values for signing
			headerValues := make(map[string]string, len(s.includeHeaders))
			for _, hdr := range s.includeHeaders {
				headerValues[hdr] = bw.Header().Get(hdr)
			}

			// Sign
			sigValue, err := s.sign(headerValues, bw.Body.Bytes())
			if err != nil {
				s.errors.Add(1)
				bw.FlushTo(w)
				return
			}

			s.totalSigned.Add(1)

			bufutil.CopyHeaders(w.Header(), bw.Header())
			w.Header().Set(s.header, sigValue)
			w.WriteHeader(bw.StatusCode)
			w.Write(bw.Body.Bytes())
		})
	}
}

// TotalSigned returns the count of successfully signed responses.
func (s *Signer) TotalSigned() int64 {
	return s.totalSigned.Load()
}

// Errors returns the count of signing errors.
func (s *Signer) Errors() int64 {
	return s.errors.Load()
}

// Algorithm returns the signing algorithm.
func (s *Signer) Algorithm() string {
	return s.algorithm
}

// SignerByRoute manages per-route response signers.
type SignerByRoute = byroute.Factory[*Signer, config.ResponseSigningConfig]

// NewSignerByRoute creates a new per-route response signer manager.
func NewSignerByRoute() *SignerByRoute {
	return byroute.NewFactory(New, func(s *Signer) any {
		return map[string]interface{}{
			"algorithm":    s.algorithm,
			"key_id":       s.keyID,
			"header":       s.header,
			"total_signed": s.TotalSigned(),
			"errors":       s.Errors(),
		}
	})
}
