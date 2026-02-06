package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/health"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/middleware/transform"
	"github.com/example/gateway/internal/retry"
	"github.com/example/gateway/internal/router"
	"github.com/example/gateway/internal/variables"
)

// Proxy handles proxying requests to backends
type Proxy struct {
	transport       *http.Transport
	healthChecker   *health.Checker
	resolver        *variables.Resolver
	defaultTimeout  time.Duration
	flushInterval   time.Duration
}

// Config holds proxy configuration
type Config struct {
	Transport      *http.Transport
	HealthChecker  *health.Checker
	DefaultTimeout time.Duration
	FlushInterval  time.Duration
}

// New creates a new proxy
func New(cfg Config) *Proxy {
	transport := cfg.Transport
	if transport == nil {
		transport = DefaultTransport()
	}

	timeout := cfg.DefaultTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	flushInterval := cfg.FlushInterval
	if flushInterval == 0 {
		flushInterval = -1 // Don't flush
	}

	return &Proxy{
		transport:      transport,
		healthChecker:  cfg.HealthChecker,
		resolver:       variables.NewResolver(),
		defaultTimeout: timeout,
		flushInterval:  flushInterval,
	}
}

// Handler returns an http.Handler that proxies requests based on the route
func (p *Proxy) Handler(route *router.Route, balancer loadbalancer.Balancer) http.Handler {
	return p.HandlerWithPolicy(route, balancer, nil)
}

// HandlerWithPolicy returns an http.Handler that proxies requests using an externally
// provided retry policy. If retryPolicy is nil, a new one is created from route config.
func (p *Proxy) HandlerWithPolicy(route *router.Route, balancer loadbalancer.Balancer, retryPolicy *retry.Policy) http.Handler {
	// Create response header transformer once per handler
	transformer := transform.NewHeaderTransformer()

	// Build retry policy for this route if not provided externally
	if retryPolicy == nil {
		if route.RetryPolicy.MaxRetries > 0 {
			retryPolicy = retry.NewPolicy(route.RetryPolicy)
		} else if route.Retries > 0 {
			retryPolicy = retry.NewPolicyFromLegacy(route.Retries, time.Duration(route.Timeout))
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.GetFromRequest(r)
		varCtx.RouteID = route.ID

		// Set timeout
		timeout := p.defaultTimeout
		if route.TimeoutPolicy.Request > 0 {
			timeout = route.TimeoutPolicy.Request
		} else if route.Timeout > 0 {
			timeout = time.Duration(route.Timeout)
		}

		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		start := time.Now()
		var resp *http.Response
		var err error
		var backendURL string

		if retryPolicy != nil && retryPolicy.Hedging != nil {
			// Hedging path: let hedging executor pick backends and send concurrent requests
			// Buffer the body so it can be reused across hedged requests
			var bodyBytes []byte
			if r.Body != nil {
				bodyBytes, err = retry.BufferBody(r)
				if err != nil {
					errors.ErrBadGateway.WithDetails("Failed to read request body").WriteJSON(w)
					return
				}
			}

			nextBackend := func() string {
				if b := balancer.Next(); b != nil {
					return b.URL
				}
				return ""
			}
			makeReq := func(target *url.URL) (*http.Request, error) {
				// Restore body for each hedged request
				if bodyBytes != nil {
					r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				}
				req := p.createProxyRequest(r, target, route, varCtx)
				return req, nil
			}
			resp, err = retryPolicy.Hedging.Execute(ctx, p.transport, nextBackend, makeReq, retryPolicy.PerTryTimeout)
			if resp != nil {
				backendURL = "" // hedging picks multiple backends
			}
		} else {
			// Standard path: single backend selection
			// Check for request-aware balancer (consistent hash, sticky sessions, traffic split)
			type requestAwareBalancer interface {
				NextForHTTPRequest(r *http.Request) (*loadbalancer.Backend, string)
			}
			var backend *loadbalancer.Backend
			if ra, ok := balancer.(requestAwareBalancer); ok {
				// Check if rules pre-assigned a traffic group
				if varCtx.TrafficGroup != "" {
					if wb, ok := balancer.(*loadbalancer.WeightedBalancer); ok {
						if tg := wb.GetGroupByName(varCtx.TrafficGroup); tg != nil {
							backend = tg.Balancer.Next()
						}
					}
				}
				if backend == nil {
					var groupName string
					backend, groupName = ra.NextForHTTPRequest(r)
					if groupName != "" {
						varCtx.TrafficGroup = groupName
					}
				}
			} else {
				backend = balancer.Next()
			}
			if backend == nil {
				errors.ErrServiceUnavailable.WithDetails("No healthy backends available").WriteJSON(w)
				return
			}
			backend.IncrActive()
			defer backend.DecrActive()
			varCtx.UpstreamAddr = backend.URL
			backendURL = backend.URL

			targetURL, parseErr := url.Parse(backend.URL)
			if parseErr != nil {
				errors.ErrBadGateway.WithDetails("Invalid backend URL").WriteJSON(w)
				return
			}

			proxyReq := p.createProxyRequest(r, targetURL, route, varCtx)
			proxyReq = proxyReq.WithContext(ctx)

			if retryPolicy != nil {
				resp, err = retryPolicy.Execute(ctx, p.transport, proxyReq)
			} else {
				resp, err = p.transport.RoundTrip(proxyReq)
			}
		}
		varCtx.UpstreamResponseTime = time.Since(start)

		// Record latency for least-response-time balancer
		if lr, ok := balancer.(interface {
			RecordLatency(string, time.Duration)
		}); ok && backendURL != "" {
			lr.RecordLatency(backendURL, varCtx.UpstreamResponseTime)
		}

		if err != nil {
			p.handleError(w, r, err, backendURL, balancer)
			return
		}
		defer resp.Body.Close()

		varCtx.UpstreamStatus = resp.StatusCode

		// Apply response transformations
		transformer.TransformResponse(w, route.Transform.Response.Headers, varCtx)

		// Copy response headers
		p.copyHeaders(w.Header(), resp.Header)

		// Write status code
		w.WriteHeader(resp.StatusCode)

		// Copy response body
		p.copyBody(w, resp.Body)
	})
}


// createProxyRequest creates the request to send to the backend
func (p *Proxy) createProxyRequest(r *http.Request, target *url.URL, route *router.Route, varCtx *variables.Context) *http.Request {
	// Build target URL
	targetURL := *target
	targetURL.Path = singleJoiningSlash(target.Path, r.URL.Path)

	// Strip prefix if configured
	if route.StripPrefix && route.PathPrefix {
		suffix := stripPrefix(route.Path, r.URL.Path)
		targetURL.Path = singleJoiningSlash(target.Path, suffix)
	}

	targetURL.RawQuery = r.URL.RawQuery

	// Create new request
	proxyReq, _ := http.NewRequest(r.Method, targetURL.String(), r.Body)

	// Copy headers
	proxyReq.Header = make(http.Header)
	for k, vv := range r.Header {
		proxyReq.Header[k] = vv
	}

	// Set Host header
	proxyReq.Host = target.Host

	// Set X-Forwarded headers
	if clientIP := variables.ExtractClientIP(r); clientIP != "" {
		if prior := proxyReq.Header.Get("X-Forwarded-For"); prior != "" {
			proxyReq.Header.Set("X-Forwarded-For", prior+", "+clientIP)
		} else {
			proxyReq.Header.Set("X-Forwarded-For", clientIP)
		}
	}

	if r.TLS != nil {
		proxyReq.Header.Set("X-Forwarded-Proto", "https")
	} else {
		proxyReq.Header.Set("X-Forwarded-Proto", "http")
	}

	proxyReq.Header.Set("X-Forwarded-Host", r.Host)

	// Remove hop-by-hop headers
	removeHopHeaders(proxyReq.Header)

	return proxyReq
}

// handleError handles proxy errors
func (p *Proxy) handleError(w http.ResponseWriter, r *http.Request, err error, backendURL string, balancer loadbalancer.Balancer) {
	// Mark backend as unhealthy
	if balancer != nil {
		balancer.MarkUnhealthy(backendURL)
	}

	if err == context.DeadlineExceeded {
		errors.ErrGatewayTimeout.WriteJSON(w)
		return
	}

	errors.ErrBadGateway.WithDetails(err.Error()).WriteJSON(w)
}

// copyHeaders copies headers from source to destination
func (p *Proxy) copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}

	// Remove hop-by-hop headers from response
	removeHopHeaders(dst)
}

// copyBody copies the response body
func (p *Proxy) copyBody(w http.ResponseWriter, body io.Reader) {
	if p.flushInterval > 0 {
		// Streaming copy with flush
		if flusher, ok := w.(http.Flusher); ok {
			for {
				_, err := io.CopyN(w, body, 32*1024)
				if err != nil {
					break
				}
				flusher.Flush()
			}
			return
		}
	}

	io.Copy(w, body)
}

// Hop-by-hop headers that should be removed
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func removeHopHeaders(header http.Header) {
	for _, h := range hopHeaders {
		header.Del(h)
	}
}

// singleJoiningSlash joins two URL paths with a single slash
func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}

// stripPrefix removes the route path prefix from the request path
func stripPrefix(pattern, path string) string {
	pattern = strings.Trim(pattern, "/")
	path = strings.Trim(path, "/")

	if pattern == "" {
		return "/" + path
	}

	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if len(pathParts) <= len(patternParts) {
		return "/"
	}

	suffix := strings.Join(pathParts[len(patternParts):], "/")
	if suffix == "" {
		return "/"
	}
	return "/" + suffix
}

// RouteProxy holds proxy configuration per route
type RouteProxy struct {
	proxy       *Proxy
	balancer    loadbalancer.Balancer
	route       *router.Route
	transformer *transform.PrecompiledTransform
	retryPolicy *retry.Policy
	handler     http.Handler
}

// NewRouteProxy creates a proxy handler for a specific route
func NewRouteProxy(proxy *Proxy, route *router.Route, backends []*loadbalancer.Backend) *RouteProxy {
	rp := &RouteProxy{
		proxy:       proxy,
		balancer:    loadbalancer.NewRoundRobin(backends),
		route:       route,
		transformer: transform.NewPrecompiledTransform(route.Transform.Request.Headers),
	}

	// Create retry policy once per route
	if route.RetryPolicy.MaxRetries > 0 {
		rp.retryPolicy = retry.NewPolicy(route.RetryPolicy)
	} else if route.Retries > 0 {
		rp.retryPolicy = retry.NewPolicyFromLegacy(route.Retries, time.Duration(route.Timeout))
	}

	// Cache the handler, passing in the same retry policy so metrics are shared
	rp.handler = proxy.HandlerWithPolicy(route, rp.balancer, rp.retryPolicy)

	return rp
}

// NewRouteProxyWithBalancer creates a proxy handler with a custom balancer (e.g., weighted)
func NewRouteProxyWithBalancer(proxy *Proxy, route *router.Route, balancer loadbalancer.Balancer) *RouteProxy {
	rp := &RouteProxy{
		proxy:       proxy,
		balancer:    balancer,
		route:       route,
		transformer: transform.NewPrecompiledTransform(route.Transform.Request.Headers),
	}

	// Create retry policy once per route
	if route.RetryPolicy.MaxRetries > 0 {
		rp.retryPolicy = retry.NewPolicy(route.RetryPolicy)
	} else if route.Retries > 0 {
		rp.retryPolicy = retry.NewPolicyFromLegacy(route.Retries, time.Duration(route.Timeout))
	}

	// Cache the handler, passing in the same retry policy so metrics are shared
	rp.handler = proxy.HandlerWithPolicy(route, rp.balancer, rp.retryPolicy)

	return rp
}

// ServeHTTP handles the request
func (rp *RouteProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rp.handler.ServeHTTP(w, r)
}

// UpdateBackends updates the backends for this route
func (rp *RouteProxy) UpdateBackends(backends []*loadbalancer.Backend) {
	rp.balancer.UpdateBackends(backends)
}

// GetBalancer returns the load balancer
func (rp *RouteProxy) GetBalancer() loadbalancer.Balancer {
	return rp.balancer
}

// GetRetryMetrics returns the retry metrics for this route (may be nil)
func (rp *RouteProxy) GetRetryMetrics() *retry.RouteRetryMetrics {
	if rp.retryPolicy != nil {
		return rp.retryPolicy.Metrics
	}
	return nil
}

// SimpleProxy creates a simple reverse proxy handler
func SimpleProxy(targetURL string) (http.Handler, error) {
	_, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("invalid target URL: %w", err)
	}

	proxy := New(Config{})
	backends := []*loadbalancer.Backend{{URL: targetURL, Weight: 1, Healthy: true}}
	balancer := loadbalancer.NewRoundRobin(backends)

	route := &router.Route{
		ID:   "simple",
		Path: "/",
		Transform: config.TransformConfig{},
	}

	return proxy.Handler(route, balancer), nil
}
