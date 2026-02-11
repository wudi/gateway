package signing

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wudi/gateway/internal/config"
)

// CompiledSigner is a pre-compiled, concurrent-safe request signer for a single route.
type CompiledSigner struct {
	routeID       string
	secret        []byte
	hashFunc      func() hash.Hash
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

	secret, err := base64.StdEncoding.DecodeString(cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("signing: invalid base64 secret: %w", err)
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("signing: secret must be at least 32 bytes (got %d)", len(secret))
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
		return nil, fmt.Errorf("signing: unsupported algorithm %q", algo)
	}

	if cfg.KeyID == "" {
		return nil, fmt.Errorf("signing: key_id is required")
	}

	prefix := cfg.HeaderPrefix
	if prefix == "" {
		prefix = "X-Gateway-"
	}

	includeBody := true
	if cfg.IncludeBody != nil {
		includeBody = *cfg.IncludeBody
	}

	// Sort signed headers for deterministic order
	headers := make([]string, len(cfg.SignedHeaders))
	copy(headers, cfg.SignedHeaders)
	sort.Strings(headers)

	return &CompiledSigner{
		routeID:       routeID,
		secret:        secret,
		hashFunc:      hashFunc,
		algorithm:     algo,
		keyID:         cfg.KeyID,
		signedHeaders: headers,
		headerPrefix:  prefix,
		includeBody:   includeBody,
	}, nil
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

	// Compute HMAC
	mac := hmac.New(s.hashFunc, s.secret)
	mac.Write([]byte(sb.String()))
	sig := hex.EncodeToString(mac.Sum(nil))

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
	merged := global
	if perRoute.Algorithm != "" {
		merged.Algorithm = perRoute.Algorithm
	}
	if perRoute.Secret != "" {
		merged.Secret = perRoute.Secret
	}
	if perRoute.KeyID != "" {
		merged.KeyID = perRoute.KeyID
	}
	if len(perRoute.SignedHeaders) > 0 {
		merged.SignedHeaders = perRoute.SignedHeaders
	}
	if perRoute.IncludeBody != nil {
		merged.IncludeBody = perRoute.IncludeBody
	}
	if perRoute.HeaderPrefix != "" {
		merged.HeaderPrefix = perRoute.HeaderPrefix
	}
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
