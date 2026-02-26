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
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/logging"
	"go.uber.org/zap"
)

// signMode distinguishes HMAC from RSA signing modes.
type signMode int

const (
	signModeHMAC   signMode = iota
	signModeRSA             // PKCS1v15
	signModeRSAPSS          // PSS
)

// CompiledSigner is a pre-compiled, concurrent-safe request signer for a single route.
type CompiledSigner struct {
	routeID       string
	secret        []byte          // HMAC secret (nil for RSA)
	privateKey    *rsa.PrivateKey  // RSA private key (nil for HMAC)
	hashFunc      func() hash.Hash
	cryptoHash    crypto.Hash      // for RSA: crypto.SHA256 or crypto.SHA512
	mode          signMode
	algorithm     string
	keyID         string
	signedHeaders []string
	headerPrefix  string
	includeBody   bool
	metrics       SigningMetrics
}

// New creates a CompiledSigner from a BackendSigningConfig.
func New(routeID string, cfg config.BackendSigningConfig) (*CompiledSigner, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("signing not enabled")
	}

	algo := cfg.Algorithm
	if algo == "" {
		algo = "hmac-sha256"
	}

	if cfg.KeyID == "" {
		return nil, fmt.Errorf("signing: key_id is required")
	}

	s := &CompiledSigner{
		routeID:   routeID,
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
	case "rsa-sha512":
		s.mode = signModeRSA
		s.hashFunc = sha512.New
		s.cryptoHash = crypto.SHA512
	case "rsa-pss-sha256":
		s.mode = signModeRSAPSS
		s.hashFunc = sha256.New
		s.cryptoHash = crypto.SHA256
	default:
		return nil, fmt.Errorf("signing: unsupported algorithm %q", algo)
	}

	// Load key material based on mode
	switch s.mode {
	case signModeHMAC:
		secret, err := base64.StdEncoding.DecodeString(cfg.Secret)
		if err != nil {
			return nil, fmt.Errorf("signing: invalid base64 secret: %w", err)
		}
		if len(secret) < 32 {
			return nil, fmt.Errorf("signing: secret must be at least 32 bytes (got %d)", len(secret))
		}
		s.secret = secret
	case signModeRSA, signModeRSAPSS:
		privKey, err := loadPrivateKey(cfg.PrivateKey, cfg.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("signing: %w", err)
		}
		s.privateKey = privKey
	}

	prefix := cfg.HeaderPrefix
	if prefix == "" {
		prefix = "X-Runway-"
	}
	s.headerPrefix = prefix

	includeBody := true
	if cfg.IncludeBody != nil {
		includeBody = *cfg.IncludeBody
	}
	s.includeBody = includeBody

	// Sort signed headers for deterministic order
	headers := make([]string, len(cfg.SignedHeaders))
	copy(headers, cfg.SignedHeaders)
	sort.Strings(headers)
	s.signedHeaders = headers

	return s, nil
}

// loadPrivateKey loads an RSA private key from inline PEM or a file path.
func loadPrivateKey(inline, file string) (*rsa.PrivateKey, error) {
	var pemData []byte
	if inline != "" {
		pemData = []byte(inline)
	} else if file != "" {
		var err error
		pemData, err = os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading private key file: %w", err)
		}
	} else {
		return nil, fmt.Errorf("private_key or private_key_file is required for RSA algorithms")
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block from private key")
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

// Sign signs the outgoing request by injecting HMAC signature headers.
func (s *CompiledSigner) Sign(r *http.Request) error {
	s.metrics.TotalRequests.Add(1)

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Hash the body (for methods that typically have one)
	var bodyHash string
	if s.includeBody && hasBody(r.Method) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			s.metrics.Errors.Add(1)
			return fmt.Errorf("signing: failed to read body: %w", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h := sha256.Sum256(body)
		bodyHash = hex.EncodeToString(h[:])
		s.metrics.BodyHashed.Add(1)
	} else {
		// Empty body hash
		h := sha256.Sum256(nil)
		bodyHash = hex.EncodeToString(h[:])
	}

	// Build signing string
	var sb strings.Builder
	sb.WriteString(r.Method)
	sb.WriteByte('\n')
	sb.WriteString(r.URL.RequestURI())
	sb.WriteByte('\n')
	sb.WriteString(timestamp)
	sb.WriteByte('\n')
	sb.WriteString(bodyHash)

	for _, hdr := range s.signedHeaders {
		sb.WriteByte('\n')
		sb.WriteString(strings.ToLower(hdr))
		sb.WriteByte(':')
		if strings.EqualFold(hdr, "Host") {
			sb.WriteString(r.Host)
		} else {
			sb.WriteString(r.Header.Get(hdr))
		}
	}

	// Compute signature based on mode
	signingString := []byte(sb.String())
	var sig string

	switch s.mode {
	case signModeHMAC:
		mac := hmac.New(s.hashFunc, s.secret)
		mac.Write(signingString)
		sig = hex.EncodeToString(mac.Sum(nil))
	case signModeRSA:
		h := s.hashFunc()
		h.Write(signingString)
		digest := h.Sum(nil)
		sigBytes, err := rsa.SignPKCS1v15(rand.Reader, s.privateKey, s.cryptoHash, digest)
		if err != nil {
			s.metrics.Errors.Add(1)
			return fmt.Errorf("signing: RSA sign failed: %w", err)
		}
		sig = hex.EncodeToString(sigBytes)
	case signModeRSAPSS:
		h := s.hashFunc()
		h.Write(signingString)
		digest := h.Sum(nil)
		sigBytes, err := rsa.SignPSS(rand.Reader, s.privateKey, s.cryptoHash, digest, nil)
		if err != nil {
			s.metrics.Errors.Add(1)
			return fmt.Errorf("signing: RSA-PSS sign failed: %w", err)
		}
		sig = hex.EncodeToString(sigBytes)
	}

	// Inject headers
	r.Header.Set(s.headerPrefix+"Signature", s.algorithm+"="+sig)
	r.Header.Set(s.headerPrefix+"Timestamp", timestamp)
	r.Header.Set(s.headerPrefix+"Key-ID", s.keyID)
	if len(s.signedHeaders) > 0 {
		r.Header.Set(s.headerPrefix+"Signed-Headers", strings.Join(s.signedHeaders, ";"))
	} else {
		r.Header.Set(s.headerPrefix+"Signed-Headers", "")
	}

	s.metrics.Signed.Add(1)
	return nil
}

// Status returns the admin API snapshot.
func (s *CompiledSigner) Status() SigningStatus {
	return SigningStatus{
		RouteID:       s.routeID,
		Algorithm:     s.algorithm,
		KeyID:         s.keyID,
		HeaderPrefix:  s.headerPrefix,
		IncludeBody:   s.includeBody,
		TotalRequests: s.metrics.TotalRequests.Load(),
		Signed:        s.metrics.Signed.Load(),
		Errors:        s.metrics.Errors.Load(),
		BodyHashed:    s.metrics.BodyHashed.Load(),
	}
}

// RouteID returns the route ID this signer is configured for.
func (s *CompiledSigner) RouteID() string {
	return s.routeID
}

// MergeSigningConfig merges per-route and global configs (per-route non-zero overrides global).
func MergeSigningConfig(perRoute, global config.BackendSigningConfig) config.BackendSigningConfig {
	merged := config.MergeNonZero(global, perRoute)
	merged.Enabled = true
	return merged
}

func hasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// Middleware returns a middleware that signs outgoing requests with HMAC before they reach the backend.
func (s *CompiledSigner) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := s.Sign(r); err != nil {
				logging.Warn("backend signing failed",
					zap.String("route_id", s.RouteID()),
					zap.Error(err),
				)
			}
			next.ServeHTTP(w, r)
		})
	}
}
