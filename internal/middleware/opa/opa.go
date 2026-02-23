package opa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/variables"
)

// OPAEnforcer evaluates requests against an OPA policy endpoint.
type OPAEnforcer struct {
	url           string
	policyPath    string
	timeout       time.Duration
	failOpen      bool
	includeBody   bool
	cacheTTL      time.Duration
	headers       []string
	client        *http.Client
	cache         sync.Map
	totalRequests atomic.Int64
	totalDenied   atomic.Int64
	totalErrors   atomic.Int64
}

// cacheEntry stores a cached OPA decision.
type cacheEntry struct {
	result bool
	expiry time.Time
}

// opaInput is the JSON structure sent to OPA.
type opaInput struct {
	Input inputData `json:"input"`
}

// inputData contains the request data sent to OPA.
type inputData struct {
	Method   string            `json:"method"`
	Path     string            `json:"path"`
	SourceIP string            `json:"source_ip"`
	Headers  map[string]string `json:"headers"`
	Identity *identityData     `json:"identity,omitempty"`
	Body     string            `json:"body,omitempty"`
}

// identityData contains authentication information.
type identityData struct {
	ClientID string                 `json:"client_id,omitempty"`
	AuthType string                 `json:"auth_type,omitempty"`
	Claims   map[string]interface{} `json:"claims,omitempty"`
}

// opaResponse is the response from OPA.
type opaResponse struct {
	Result bool `json:"result"`
}

// New creates a new OPAEnforcer from config.
func New(cfg config.OPAConfig) (*OPAEnforcer, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("opa: url is required")
	}
	if cfg.PolicyPath == "" {
		return nil, fmt.Errorf("opa: policy_path is required")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	return &OPAEnforcer{
		url:         cfg.URL,
		policyPath:  cfg.PolicyPath,
		timeout:     timeout,
		failOpen:    cfg.FailOpen,
		includeBody: cfg.IncludeBody,
		cacheTTL:    cfg.CacheTTL,
		headers:     cfg.Headers,
		client:      &http.Client{Timeout: timeout},
	}, nil
}

// Evaluate checks the request against the OPA policy.
func (e *OPAEnforcer) Evaluate(r *http.Request) (bool, error) {
	e.totalRequests.Add(1)

	// Check cache
	if e.cacheTTL > 0 {
		key := e.cacheKey(r)
		if entry, ok := e.cache.Load(key); ok {
			ce := entry.(*cacheEntry)
			if time.Now().Before(ce.expiry) {
				if !ce.result {
					e.totalDenied.Add(1)
				}
				return ce.result, nil
			}
			e.cache.Delete(key)
		}
	}

	// Build OPA input
	input, err := e.buildInput(r)
	if err != nil {
		e.totalErrors.Add(1)
		if e.failOpen {
			return true, nil
		}
		return false, fmt.Errorf("opa: build input: %w", err)
	}

	// Send request to OPA
	allowed, err := e.query(r.Context(), input)
	if err != nil {
		e.totalErrors.Add(1)
		if e.failOpen {
			return true, nil
		}
		return false, err
	}

	if !allowed {
		e.totalDenied.Add(1)
	}

	// Cache result
	if e.cacheTTL > 0 {
		key := e.cacheKey(r)
		e.cache.Store(key, &cacheEntry{
			result: allowed,
			expiry: time.Now().Add(e.cacheTTL),
		})
	}

	return allowed, nil
}

// buildInput constructs the OPA input from the request.
func (e *OPAEnforcer) buildInput(r *http.Request) (*opaInput, error) {
	clientIP := variables.ExtractClientIP(r)

	headers := make(map[string]string)
	if len(e.headers) > 0 {
		for _, h := range e.headers {
			if v := r.Header.Get(h); v != "" {
				headers[http.CanonicalHeaderKey(h)] = v
			}
		}
	} else {
		for k := range r.Header {
			headers[k] = r.Header.Get(k)
		}
	}

	input := &opaInput{
		Input: inputData{
			Method:   r.Method,
			Path:     r.URL.Path,
			SourceIP: clientIP,
			Headers:  headers,
		},
	}

	// Add identity from variables context
	v := variables.GetFromRequest(r)
	if v.Identity != nil {
		input.Input.Identity = &identityData{
			ClientID: v.Identity.ClientID,
			AuthType: v.Identity.AuthType,
			Claims:   v.Identity.Claims,
		}
	}

	// Optionally include request body
	if e.includeBody && r.Body != nil {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
		if err != nil {
			return nil, fmt.Errorf("read body: %w", err)
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		input.Input.Body = string(body)
	}

	return input, nil
}

// query sends the input to the OPA server and returns the decision.
func (e *OPAEnforcer) query(ctx context.Context, input *opaInput) (bool, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return false, fmt.Errorf("opa: marshal input: %w", err)
	}

	url := fmt.Sprintf("%s/v1/data/%s", e.url, e.policyPath)

	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("opa: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("opa: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("opa: unexpected status %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, fmt.Errorf("opa: read response: %w", err)
	}

	var opaResp opaResponse
	if err := json.Unmarshal(respBody, &opaResp); err != nil {
		return false, fmt.Errorf("opa: unmarshal response: %w", err)
	}

	return opaResp.Result, nil
}

// cacheKey builds a cache key from the request.
func (e *OPAEnforcer) cacheKey(r *http.Request) string {
	clientIP := variables.ExtractClientIP(r)
	return r.Method + "|" + r.URL.Path + "|" + clientIP
}

// Middleware returns a middleware that evaluates requests against the OPA policy.
func (e *OPAEnforcer) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, err := e.Evaluate(r)
			if err != nil {
				http.Error(w, `{"error":"policy evaluation failed"}`, http.StatusForbidden)
				return
			}
			if !allowed {
				http.Error(w, `{"error":"request denied by policy"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TotalRequests returns the total number of policy evaluations.
func (e *OPAEnforcer) TotalRequests() int64 {
	return e.totalRequests.Load()
}

// TotalDenied returns the total number of denied requests.
func (e *OPAEnforcer) TotalDenied() int64 {
	return e.totalDenied.Load()
}

// TotalErrors returns the total number of OPA errors.
func (e *OPAEnforcer) TotalErrors() int64 {
	return e.totalErrors.Load()
}

// OPAByRoute manages per-route OPA enforcers.
type OPAByRoute struct {
	byroute.Manager[*OPAEnforcer]
}

// NewOPAByRoute creates a new per-route OPA enforcer manager.
func NewOPAByRoute() *OPAByRoute {
	return &OPAByRoute{}
}

// AddRoute adds an OPA enforcer for a route.
func (m *OPAByRoute) AddRoute(routeID string, cfg config.OPAConfig) error {
	enforcer, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, enforcer)
	return nil
}

// GetEnforcer returns the OPA enforcer for a route.
func (m *OPAByRoute) GetEnforcer(routeID string) *OPAEnforcer {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route OPA stats.
func (m *OPAByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(e *OPAEnforcer) interface{} {
		return map[string]interface{}{
			"total_requests": e.TotalRequests(),
			"total_denied":   e.TotalDenied(),
			"total_errors":   e.TotalErrors(),
		}
	})
}
