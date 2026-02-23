package responsesigning

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
	"encoding/pem"
	"fmt"
	"hash"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
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

// bufferingWriter captures status, headers, and body from the inner handler.
type bufferingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
	header http.Header
}

func newBufferingWriter(w http.ResponseWriter) *bufferingWriter {
	return &bufferingWriter{
		ResponseWriter: w,
		status:         http.StatusOK,
		header:         make(http.Header),
	}
}

func (bw *bufferingWriter) Header() http.Header {
	return bw.header
}

func (bw *bufferingWriter) WriteHeader(code int) {
	bw.status = code
}

func (bw *bufferingWriter) Write(b []byte) (int, error) {
	return bw.body.Write(b)
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
			bw := newBufferingWriter(w)

			// Run the inner handler, capturing the response
			next.ServeHTTP(bw, r)

			// Collect header values for signing
			headerValues := make(map[string]string, len(s.includeHeaders))
			for _, hdr := range s.includeHeaders {
				headerValues[hdr] = bw.header.Get(hdr)
			}

			// Sign
			sigValue, err := s.sign(headerValues, bw.body.Bytes())
			if err != nil {
				s.errors.Add(1)
				// Still flush buffered response even on signing error
				flushBuffered(w, bw)
				return
			}

			s.totalSigned.Add(1)

			// Merge buffered headers to actual response writer
			for k, vals := range bw.header {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}

			// Set the signature header
			w.Header().Set(s.header, sigValue)

			// Write status and body
			w.WriteHeader(bw.status)
			w.Write(bw.body.Bytes())
		})
	}
}

// flushBuffered writes the buffered response to the actual writer without signature.
func flushBuffered(w http.ResponseWriter, bw *bufferingWriter) {
	for k, vals := range bw.header {
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(bw.status)
	w.Write(bw.body.Bytes())
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
type SignerByRoute struct {
	byroute.Manager[*Signer]
}

// NewSignerByRoute creates a new per-route response signer manager.
func NewSignerByRoute() *SignerByRoute {
	return &SignerByRoute{}
}

// AddRoute registers a response signer for a route.
func (m *SignerByRoute) AddRoute(routeID string, cfg config.ResponseSigningConfig) error {
	s, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, s)
	return nil
}

// GetSigner returns the signer for a route, or nil if none.
func (m *SignerByRoute) GetSigner(routeID string) *Signer {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route response signing stats.
func (m *SignerByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(s *Signer) interface{} {
		return map[string]interface{}{
			"algorithm":    s.algorithm,
			"key_id":       s.keyID,
			"header":       s.header,
			"total_signed": s.TotalSigned(),
			"errors":       s.Errors(),
		}
	})
}
