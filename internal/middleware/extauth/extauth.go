package extauth

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
)

// ExtAuth handles external authentication by delegating to an HTTP or gRPC service.
type ExtAuth struct {
	url             string
	protocol        string // "http" or "grpc"
	timeout         time.Duration
	failOpen        bool
	headersToSend   map[string]bool // nil = send all
	headersToInject map[string]bool // nil = inject all from response
	httpClient      *http.Client
	grpcConn        *grpc.ClientConn
	cache           *authCache
	metrics         *ExtAuthMetrics
}

// ExtAuthResult represents the result of an external auth check.
type ExtAuthResult struct {
	Allowed         bool
	DeniedStatus    int               // HTTP status to return on deny
	DeniedBody      []byte            // response body to return on deny
	DeniedHeaders   http.Header       // response headers to return on deny
	HeadersToInject map[string]string // headers to add to upstream request on allow
}

// CheckRequest is the JSON body sent to the ext auth HTTP service.
type CheckRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

// CheckResponse is the JSON response from a gRPC ext auth service.
type CheckResponse struct {
	Allowed       bool              `json:"allowed"`
	DeniedStatus  int               `json:"denied_status,omitempty"`
	DeniedBody    string            `json:"denied_body,omitempty"`
	Headers       map[string]string `json:"headers,omitempty"`        // headers to inject on allow
	DeniedHeaders map[string]string `json:"denied_headers,omitempty"` // headers to return on deny
}

// jsonCodec is a gRPC codec that marshals/unmarshals JSON.
type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

func (jsonCodec) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

func (jsonCodec) Name() string {
	return "json"
}

// New creates a new ExtAuth client from config.
func New(cfg config.ExtAuthConfig) (*ExtAuth, error) {
	ea := &ExtAuth{
		url:      cfg.URL,
		failOpen: cfg.FailOpen,
		timeout:  cfg.Timeout,
		metrics:  NewExtAuthMetrics(),
	}

	if ea.timeout == 0 {
		ea.timeout = 5 * time.Second
	}

	// Determine protocol
	if strings.HasPrefix(cfg.URL, "grpc://") {
		ea.protocol = "grpc"
	} else {
		ea.protocol = "http"
	}

	// Build header sets
	if len(cfg.HeadersToSend) > 0 {
		ea.headersToSend = make(map[string]bool, len(cfg.HeadersToSend))
		for _, h := range cfg.HeadersToSend {
			ea.headersToSend[http.CanonicalHeaderKey(h)] = true
		}
	}
	if len(cfg.HeadersToInject) > 0 {
		ea.headersToInject = make(map[string]bool, len(cfg.HeadersToInject))
		for _, h := range cfg.HeadersToInject {
			ea.headersToInject[http.CanonicalHeaderKey(h)] = true
		}
	}

	// Initialize cache
	if cfg.CacheTTL > 0 {
		ea.cache = newAuthCache(cfg.CacheTTL)
	}

	// Initialize transport
	if ea.protocol == "grpc" {
		conn, err := dialGRPC(cfg)
		if err != nil {
			return nil, fmt.Errorf("ext_auth grpc dial: %w", err)
		}
		ea.grpcConn = conn
	} else {
		ea.httpClient = &http.Client{
			Timeout: ea.timeout,
		}
		if cfg.TLS.Enabled {
			tlsConfig, err := buildHTTPTLSConfig(cfg.TLS)
			if err != nil {
				return nil, fmt.Errorf("ext_auth tls: %w", err)
			}
			ea.httpClient.Transport = &http.Transport{TLSClientConfig: tlsConfig}
		}
	}

	return ea, nil
}

// Check performs an auth check against the external service.
func (ea *ExtAuth) Check(r *http.Request) (*ExtAuthResult, error) {
	// Check cache
	var cacheKey string
	if ea.cache != nil {
		cacheKey = BuildKey(r, ea.headersToSend)
		if cached := ea.cache.Get(cacheKey); cached != nil {
			ea.metrics.RecordCacheHit()
			return cached, nil
		}
	}

	start := time.Now()
	var result *ExtAuthResult
	var err error

	if ea.protocol == "grpc" {
		result, err = ea.checkGRPC(r)
	} else {
		result, err = ea.checkHTTP(r)
	}

	if err != nil {
		ea.metrics.RecordError()
		if ea.failOpen {
			return &ExtAuthResult{Allowed: true}, nil
		}
		return nil, err
	}

	ea.metrics.Record(result.Allowed, time.Since(start))

	// Cache successful allow results
	if ea.cache != nil && result.Allowed {
		ea.cache.Set(cacheKey, result)
	}

	return result, nil
}

func (ea *ExtAuth) checkHTTP(r *http.Request) (*ExtAuthResult, error) {
	// Build check request body
	reqHeaders := ea.collectHeaders(r)
	checkReq := CheckRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: reqHeaders,
	}

	body, err := json.Marshal(checkReq)
	if err != nil {
		return nil, fmt.Errorf("marshal check request: %w", err)
	}

	ctx, cancel := context.WithTimeout(r.Context(), ea.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, ea.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create auth request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// Forward selected request headers
	for k, v := range reqHeaders {
		httpReq.Header.Set(k, v)
	}

	resp, err := ea.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("auth service request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit

	if resp.StatusCode == http.StatusOK {
		// Allowed — extract headers to inject
		result := &ExtAuthResult{
			Allowed:         true,
			HeadersToInject: make(map[string]string),
		}
		for k, vv := range resp.Header {
			if ea.shouldInjectHeader(k) {
				result.HeadersToInject[k] = vv[0]
			}
		}
		return result, nil
	}

	// Denied
	result := &ExtAuthResult{
		Allowed:       false,
		DeniedStatus:  resp.StatusCode,
		DeniedBody:    respBody,
		DeniedHeaders: make(http.Header),
	}
	for k, vv := range resp.Header {
		if k != "Content-Length" && k != "Transfer-Encoding" {
			result.DeniedHeaders[k] = vv
		}
	}
	return result, nil
}

func (ea *ExtAuth) checkGRPC(r *http.Request) (*ExtAuthResult, error) {
	reqHeaders := ea.collectHeaders(r)
	checkReq := &CheckRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: reqHeaders,
	}

	ctx, cancel := context.WithTimeout(r.Context(), ea.timeout)
	defer cancel()

	var resp CheckResponse
	err := ea.grpcConn.Invoke(ctx, "/extauth.AuthService/Check", checkReq, &resp)
	if err != nil {
		return nil, fmt.Errorf("grpc auth check: %w", err)
	}

	if resp.Allowed {
		result := &ExtAuthResult{
			Allowed:         true,
			HeadersToInject: make(map[string]string),
		}
		for k, v := range resp.Headers {
			if ea.shouldInjectHeader(k) {
				result.HeadersToInject[k] = v
			}
		}
		return result, nil
	}

	// Denied
	result := &ExtAuthResult{
		Allowed:       false,
		DeniedStatus:  resp.DeniedStatus,
		DeniedBody:    []byte(resp.DeniedBody),
		DeniedHeaders: make(http.Header),
	}
	for k, v := range resp.DeniedHeaders {
		result.DeniedHeaders.Set(k, v)
	}
	return result, nil
}

func (ea *ExtAuth) collectHeaders(r *http.Request) map[string]string {
	headers := make(map[string]string)
	if ea.headersToSend == nil {
		// Send all
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}
	} else {
		for k := range ea.headersToSend {
			if v := r.Header.Get(k); v != "" {
				headers[k] = v
			}
		}
	}
	return headers
}

func (ea *ExtAuth) shouldInjectHeader(key string) bool {
	if ea.headersToInject == nil {
		return true // inject all
	}
	return ea.headersToInject[http.CanonicalHeaderKey(key)]
}

// Close closes any gRPC connections.
func (ea *ExtAuth) Close() {
	if ea.grpcConn != nil {
		ea.grpcConn.Close()
	}
}

func dialGRPC(cfg config.ExtAuthConfig) (*grpc.ClientConn, error) {
	target := strings.TrimPrefix(cfg.URL, "grpc://")

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithDefaultCallOptions(grpc.ForceCodec(jsonCodec{})))

	if cfg.TLS.Enabled {
		creds, err := buildGRPCTLSCredentials(cfg.TLS)
		if err != nil {
			return nil, err
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.Dial(target, opts...) //nolint:staticcheck // grpc.Dial for compatibility
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func buildGRPCTLSCredentials(cfg config.ExtAuthTLSConfig) (credentials.TransportCredentials, error) {
	tlsConfig := &tls.Config{}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = certPool
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return credentials.NewTLS(tlsConfig), nil
}

func buildHTTPTLSConfig(cfg config.ExtAuthTLSConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{}

	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = certPool
	}

	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// Middleware returns a middleware that calls an external auth service before allowing the request.
func (ea *ExtAuth) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result, err := ea.Check(r)
			if err != nil {
				errors.ErrBadGateway.WriteJSON(w)
				return
			}
			if !result.Allowed {
				for k, vv := range result.DeniedHeaders {
					for _, v := range vv {
						w.Header().Add(k, v)
					}
				}
				status := result.DeniedStatus
				if status == 0 {
					status = http.StatusForbidden
				}
				w.WriteHeader(status)
				if len(result.DeniedBody) > 0 {
					w.Write(result.DeniedBody)
				}
				return
			}
			for k, v := range result.HeadersToInject {
				r.Header.Set(k, v)
			}
			next.ServeHTTP(w, r)
		})
	}
}
