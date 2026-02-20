package inboundsigning

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math"
	"net/http"
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

// CompiledVerifier is a pre-compiled, concurrent-safe inbound request signature verifier.
type CompiledVerifier struct {
	routeID       string
	secret        []byte
	hashFunc      func() hash.Hash
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

	secret, err := base64.StdEncoding.DecodeString(cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("inbound signing: invalid base64 secret: %w", err)
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("inbound signing: secret must be at least 32 bytes (got %d)", len(secret))
	}

	algo := cfg.Algorithm
	if algo == "" {
		algo = "hmac-sha256"
	}

	var hashFunc func() hash.Hash
	switch algo {
	case "hmac-sha256":
		hashFunc = sha256.New
	case "hmac-sha512":
		hashFunc = sha512.New
	default:
		return nil, fmt.Errorf("inbound signing: unsupported algorithm %q", algo)
	}

	prefix := cfg.HeaderPrefix
	if prefix == "" {
		prefix = "X-Gateway-"
	}

	includeBody := true
	if cfg.IncludeBody != nil {
		includeBody = *cfg.IncludeBody
	}

	maxAge := cfg.MaxAge
	if maxAge == 0 {
		maxAge = 5 * time.Minute
	}

	headers := make([]string, len(cfg.SignedHeaders))
	copy(headers, cfg.SignedHeaders)
	sort.Strings(headers)

	return &CompiledVerifier{
		routeID:       routeID,
		secret:        secret,
		hashFunc:      hashFunc,
		algorithm:     algo,
		keyID:         cfg.KeyID,
		signedHeaders: headers,
		headerPrefix:  prefix,
		includeBody:   includeBody,
		maxAge:        maxAge,
		shadowMode:    cfg.ShadowMode,
	}, nil
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

	// Compute HMAC and compare
	mac := hmac.New(v.hashFunc, v.secret)
	mac.Write([]byte(sb.String()))
	computedSig := mac.Sum(nil)

	if !hmac.Equal(computedSig, expectedSig) {
		v.metrics.Rejected.Add(1)
		return fmt.Errorf("signature mismatch")
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
