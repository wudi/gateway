package inboundsigning

import (
	"bytes"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"hash"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
	"go.uber.org/zap"
)

// signMode distinguishes HMAC from RSA verification modes.
type signMode int

const (
	signModeHMAC   signMode = iota
	signModeRSA             // PKCS1v15
	signModeRSAPSS          // PSS
)

// CompiledVerifier is a pre-compiled, concurrent-safe inbound request signature verifier.
type CompiledVerifier struct {
	routeID       string
	secret        []byte          // HMAC secret (nil for RSA)
	publicKey     *rsa.PublicKey   // RSA public key (nil for HMAC)
	hashFunc      func() hash.Hash
	cryptoHash    crypto.Hash      // for RSA: crypto.SHA256 or crypto.SHA512
	mode          signMode
	algorithm     string
	keyID         string
	signedHeaders []string
	headerPrefix  string
	includeBody   bool
	maxAge        time.Duration
	shadowMode    bool
	metrics       VerifierMetrics
}

// VerifierMetrics tracks per-route verification activity.
type VerifierMetrics struct {
	TotalRequests atomic.Int64
	Verified      atomic.Int64
	Rejected      atomic.Int64
	Expired       atomic.Int64
	Errors        atomic.Int64
}

// VerifierStatus is the admin API snapshot.
type VerifierStatus struct {
	RouteID       string `json:"route_id"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	HeaderPrefix  string `json:"header_prefix"`
	IncludeBody   bool   `json:"include_body"`
	MaxAge        string `json:"max_age"`
	ShadowMode    bool   `json:"shadow_mode"`
	TotalRequests int64  `json:"total_requests"`
	Verified      int64  `json:"verified"`
	Rejected      int64  `json:"rejected"`
	Expired       int64  `json:"expired"`
	Errors        int64  `json:"errors"`
}

// New creates a CompiledVerifier from config.
func New(routeID string, cfg config.InboundSigningConfig) (*CompiledVerifier, error) {
	if !cfg.Enabled {
		return nil, fmt.Errorf("inbound signing not enabled")
	}

	algo := cfg.Algorithm
	if algo == "" {
		algo = "hmac-sha256"
	}

	v := &CompiledVerifier{
		routeID:   routeID,
		algorithm: algo,
		keyID:     cfg.KeyID,
		shadowMode: cfg.ShadowMode,
	}

	switch algo {
	case "hmac-sha256":
		v.mode = signModeHMAC
		v.hashFunc = sha256.New
	case "hmac-sha512":
		v.mode = signModeHMAC
		v.hashFunc = sha512.New
	case "rsa-sha256":
		v.mode = signModeRSA
		v.hashFunc = sha256.New
		v.cryptoHash = crypto.SHA256
	case "rsa-sha512":
		v.mode = signModeRSA
		v.hashFunc = sha512.New
		v.cryptoHash = crypto.SHA512
	case "rsa-pss-sha256":
		v.mode = signModeRSAPSS
		v.hashFunc = sha256.New
		v.cryptoHash = crypto.SHA256
	default:
		return nil, fmt.Errorf("inbound signing: unsupported algorithm %q", algo)
	}

	// Load key material based on mode
	switch v.mode {
	case signModeHMAC:
		secret, err := base64.StdEncoding.DecodeString(cfg.Secret)
		if err != nil {
			return nil, fmt.Errorf("inbound signing: invalid base64 secret: %w", err)
		}
		if len(secret) < 32 {
			return nil, fmt.Errorf("inbound signing: secret must be at least 32 bytes (got %d)", len(secret))
		}
		v.secret = secret
	case signModeRSA, signModeRSAPSS:
		pubKey, err := loadPublicKey(cfg.PublicKey, cfg.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("inbound signing: %w", err)
		}
		v.publicKey = pubKey
	}

	prefix := cfg.HeaderPrefix
	if prefix == "" {
		prefix = "X-Gateway-"
	}
	v.headerPrefix = prefix

	includeBody := true
	if cfg.IncludeBody != nil {
		includeBody = *cfg.IncludeBody
	}
	v.includeBody = includeBody

	maxAge := cfg.MaxAge
	if maxAge == 0 {
		maxAge = 5 * time.Minute
	}
	v.maxAge = maxAge

	headers := make([]string, len(cfg.SignedHeaders))
	copy(headers, cfg.SignedHeaders)
	sort.Strings(headers)
	v.signedHeaders = headers

	return v, nil
}

// loadPublicKey loads an RSA public key from inline PEM or a file path.
func loadPublicKey(inline, file string) (*rsa.PublicKey, error) {
	var pemData []byte
	if inline != "" {
		pemData = []byte(inline)
	} else if file != "" {
		var err error
		pemData, err = os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading public key file: %w", err)
		}
	} else {
		return nil, fmt.Errorf("public_key or public_key_file is required for RSA algorithms")
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block from public key")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not RSA (got %T)", pub)
	}
	return rsaPub, nil
}

// Verify verifies the inbound request signature. Returns nil if valid.
func (v *CompiledVerifier) Verify(r *http.Request) error {
	v.metrics.TotalRequests.Add(1)

	// Parse timestamp header
	tsStr := r.Header.Get(v.headerPrefix + "Timestamp")
	if tsStr == "" {
		v.metrics.Rejected.Add(1)
		return fmt.Errorf("missing %sTimestamp header", v.headerPrefix)
	}
	tsInt, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		v.metrics.Rejected.Add(1)
		return fmt.Errorf("invalid %sTimestamp header", v.headerPrefix)
	}

	// Check timestamp age
	age := time.Duration(math.Abs(float64(time.Now().Unix()-tsInt))) * time.Second
	if age > v.maxAge {
		v.metrics.Expired.Add(1)
		return fmt.Errorf("timestamp expired (age %s > max %s)", age, v.maxAge)
	}

	// Optionally verify key ID
	if v.keyID != "" {
		kid := r.Header.Get(v.headerPrefix + "Key-ID")
		if kid != v.keyID {
			v.metrics.Rejected.Add(1)
			return fmt.Errorf("key ID mismatch: got %q, expected %q", kid, v.keyID)
		}
	}

	// Parse signature header
	sigHeader := r.Header.Get(v.headerPrefix + "Signature")
	if sigHeader == "" {
		v.metrics.Rejected.Add(1)
		return fmt.Errorf("missing %sSignature header", v.headerPrefix)
	}
	parts := strings.SplitN(sigHeader, "=", 2)
	if len(parts) != 2 || parts[0] != v.algorithm {
		v.metrics.Rejected.Add(1)
		return fmt.Errorf("invalid signature format or algorithm mismatch")
	}
	expectedSig, err := hex.DecodeString(parts[1])
	if err != nil {
		v.metrics.Rejected.Add(1)
		return fmt.Errorf("invalid signature hex encoding")
	}

	// Hash the body
	var bodyHash string
	if v.includeBody && hasBody(r.Method) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			v.metrics.Errors.Add(1)
			return fmt.Errorf("failed to read body: %w", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h := sha256.Sum256(body)
		bodyHash = hex.EncodeToString(h[:])
	} else {
		h := sha256.Sum256(nil)
		bodyHash = hex.EncodeToString(h[:])
	}

	// Build signing string (same format as outbound signing)
	var sb strings.Builder
	sb.WriteString(r.Method)
	sb.WriteByte('\n')
	sb.WriteString(r.URL.RequestURI())
	sb.WriteByte('\n')
	sb.WriteString(tsStr)
	sb.WriteByte('\n')
	sb.WriteString(bodyHash)

	for _, hdr := range v.signedHeaders {
		sb.WriteByte('\n')
		sb.WriteString(strings.ToLower(hdr))
		sb.WriteByte(':')
		if strings.EqualFold(hdr, "Host") {
			sb.WriteString(r.Host)
		} else {
			sb.WriteString(r.Header.Get(hdr))
		}
	}

	// Verify signature based on mode
	signingString := []byte(sb.String())

	switch v.mode {
	case signModeHMAC:
		mac := hmac.New(v.hashFunc, v.secret)
		mac.Write(signingString)
		computedSig := mac.Sum(nil)
		if !hmac.Equal(computedSig, expectedSig) {
			v.metrics.Rejected.Add(1)
			return fmt.Errorf("signature mismatch")
		}
	case signModeRSA:
		h := v.hashFunc()
		h.Write(signingString)
		digest := h.Sum(nil)
		if err := rsa.VerifyPKCS1v15(v.publicKey, v.cryptoHash, digest, expectedSig); err != nil {
			v.metrics.Rejected.Add(1)
			return fmt.Errorf("signature mismatch")
		}
	case signModeRSAPSS:
		h := v.hashFunc()
		h.Write(signingString)
		digest := h.Sum(nil)
		if err := rsa.VerifyPSS(v.publicKey, v.cryptoHash, digest, expectedSig, nil); err != nil {
			v.metrics.Rejected.Add(1)
			return fmt.Errorf("signature mismatch")
		}
	}

	v.metrics.Verified.Add(1)
	return nil
}

// Status returns the admin API snapshot.
func (v *CompiledVerifier) Status() VerifierStatus {
	return VerifierStatus{
		RouteID:       v.routeID,
		Algorithm:     v.algorithm,
		KeyID:         v.keyID,
		HeaderPrefix:  v.headerPrefix,
		IncludeBody:   v.includeBody,
		MaxAge:        v.maxAge.String(),
		ShadowMode:    v.shadowMode,
		TotalRequests: v.metrics.TotalRequests.Load(),
		Verified:      v.metrics.Verified.Load(),
		Rejected:      v.metrics.Rejected.Load(),
		Expired:       v.metrics.Expired.Load(),
		Errors:        v.metrics.Errors.Load(),
	}
}

// Middleware returns a middleware that verifies inbound request signatures.
func (v *CompiledVerifier) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := v.Verify(r); err != nil {
				if v.shadowMode {
					logging.Warn("inbound signature verification failed (shadow mode)",
						zap.String("route_id", v.routeID),
						zap.Error(err),
					)
					next.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{
					"error": "signature verification failed",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MergeInboundSigningConfig merges per-route and global configs.
func MergeInboundSigningConfig(perRoute, global config.InboundSigningConfig) config.InboundSigningConfig {
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
