package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/health"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/middleware/transform"
	"github.com/wudi/runway/internal/retry"
	"github.com/wudi/runway/internal/router"
	"github.com/wudi/runway/variables"
)

// Proxy handles proxying requests to backends
type Proxy struct {
	transportPool  *TransportPool
	healthChecker  *health.Checker
	resolver       *variables.Resolver
	defaultTimeout time.Duration
	flushInterval  time.Duration
}

// Config holds proxy configuration
type Config struct {
	Transport      http.RoundTripper // deprecated: use TransportPool
	TransportPool  *TransportPool
	HealthChecker  *health.Checker
	DefaultTimeout time.Duration
	FlushInterval  time.Duration
}

// New creates a new proxy
func New(cfg Config) *Proxy {
	pool := cfg.TransportPool
	if pool == nil {
		if cfg.Transport != nil {
			pool = &TransportPool{
				defaultTransport: cfg.Transport,
				transports:       make(map[string]http.RoundTripper),
			}
		} else {
			pool = NewTransportPool()
		}
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
		transportPool:  pool,
		healthChecker:  cfg.HealthChecker,
		resolver:       variables.NewResolver(),
		defaultTimeout: timeout,
		flushInterval:  flushInterval,
	}
}

// GetTransportPool returns the transport pool.
func (p *Proxy) GetTransportPool() *TransportPool {
	return p.transportPool
}

// SetTransportPool replaces the transport pool (used during config reload).
func (p *Proxy) SetTransportPool(pool *TransportPool) {
	p.transportPool = pool
}

// Handler returns an http.Handler that proxies requests based on the route
func (p *Proxy) Handler(route *router.Route, balancer loadbalancer.Balancer) http.Handler {
	return p.HandlerWithPolicy(route, balancer, nil)
}

// HandlerWithPolicy returns an http.Handler that proxies requests using an externally
// provided retry policy. If retryPolicy is nil, a new one is created from route config.
// transportOverride, if non-nil, replaces the default transport (e.g., for redirect following).
func (p *Proxy) HandlerWithPolicy(route *router.Route, balancer loadbalancer.Balancer, retryPolicy *retry.Policy, transportOverride ...http.RoundTripper) http.Handler {
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

	// Resolve transport for this route's upstream once per handler creation
	var transport http.RoundTripper
	if len(transportOverride) > 0 && transportOverride[0] != nil {
		transport = transportOverride[0]
	} else {
		transport = p.transportPool.Get(route.UpstreamName)
	}

	// Cache interface type assertions once per handler creation (not per-request)
	reqAwareBalancer, isRequestAware := balancer.(loadbalancer.RequestAwareBalancer)
	weightedBalancer, _ := balancer.(*loadbalancer.WeightedBalancer)
	latencyRecorder, _ := balancer.(loadbalancer.LatencyRecorder)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.GetFromRequest(r)
		varCtx.RouteID = route.ID

		// Set timeout: only create a new context deadline if the incoming context
		// has none (i.e., no timeout middleware already set one).
		ctx := r.Context()
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			timeout := p.defaultTimeout
			if route.TimeoutPolicy.Request > 0 {
				timeout = route.TimeoutPolicy.Request
			} else if route.Timeout > 0 {
				timeout = time.Duration(route.Timeout)
			}
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

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
				req := p.createProxyRequest(r.Context(), r, target, route, varCtx, nil)
				return req, nil
			}
			resp, err = retryPolicy.Hedging.Execute(ctx, transport, nextBackend, makeReq, retryPolicy.PerTryTimeout)
			if resp != nil {
				backendURL = "" // hedging picks multiple backends
			}
		} else {
			// Standard path: single backend selection
			var backend *loadbalancer.Backend

			// Check for switch_backend override from rule actions
			if varCtx.Overrides != nil && varCtx.Overrides.SwitchBackend != "" {
				if b := balancer.GetBackendByURL(varCtx.Overrides.SwitchBackend); b != nil && b.Healthy {
					backend = b
				}
			}

			if backend == nil && isRequestAware {
				// Check if rules pre-assigned a traffic group
				if varCtx.TrafficGroup != "" && weightedBalancer != nil {
					if tg := weightedBalancer.GetGroupByName(varCtx.TrafficGroup); tg != nil {
						backend = tg.Balancer.Next()
					}
				}
				if backend == nil {
					var groupName string
					backend, groupName = reqAwareBalancer.NextForHTTPRequest(r)
					if groupName != "" {
						varCtx.TrafficGroup = groupName
					}
				}
			} else if backend == nil {
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

			targetURL := backend.ParsedURL
			if targetURL == nil {
				var parseErr error
				targetURL, parseErr = url.Parse(backend.URL)
				if parseErr != nil {
					errors.ErrBadGateway.WithDetails("Invalid backend URL").WriteJSON(w)
					return
				}
			}

			pooledHeader := acquireProxyHeader()
			defer releaseProxyHeader(pooledHeader)
			proxyReq := p.createProxyRequest(ctx, r, targetURL, route, varCtx, pooledHeader)

			if retryPolicy != nil {
				resp, err = retryPolicy.Execute(ctx, transport, proxyReq)
			} else {
				// Apply backend timeout for non-retry path
				if route.TimeoutPolicy.Backend > 0 {
					tryCtx, tryCancel := context.WithTimeout(ctx, route.TimeoutPolicy.Backend)
					defer tryCancel()
					proxyReq = proxyReq.WithContext(tryCtx)
				}
				resp, err = transport.RoundTrip(proxyReq)
			}
		}
		varCtx.UpstreamResponseTime = time.Since(start)

		// Record latency for least-response-time balancer
		if latencyRecorder != nil && backendURL != "" {
			latencyRecorder.RecordLatency(backendURL, varCtx.UpstreamResponseTime)
		}

		if err != nil {
			p.handleError(w, r, err, backendURL, balancer)
			return
		}
		defer resp.Body.Close()

		// Wrap response body with idle timeout reader if configured
		if route.TimeoutPolicy.Idle > 0 {
			resp.Body = newIdleTimeoutReader(resp.Body, route.TimeoutPolicy.Idle)
		}

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

// Pre-allocated header values for X-Forwarded-Proto.
var (
	xForwardedProtoHTTP  = []string{"http"}
	xForwardedProtoHTTPS = []string{"https"}
)

var proxyHeaderPool = sync.Pool{
	New: func() any { return make(http.Header, 16) },
}

func acquireProxyHeader() http.Header {
	h := proxyHeaderPool.Get().(http.Header)
	clear(h)
	return h
}

func releaseProxyHeader(h http.Header) {
	if h == nil {
		return
	}
	// Only return reasonably-sized maps to avoid holding oversized maps
	if len(h) <= 64 {
		proxyHeaderPool.Put(h)
	}
}

// createProxyRequest creates the request to send to the backend.
// ctx is attached to the returned request directly (single WithContext call).
// If header is non-nil it is reused (caller owns pool lifecycle); otherwise a fresh map is allocated.
func (p *Proxy) createProxyRequest(ctx context.Context, r *http.Request, target *url.URL, route *router.Route, varCtx *variables.Context, header http.Header) *http.Request {
	// Build target URL
	targetURL := *target

	// Full URL override takes precedence over all other rewrite modes
	if route.HasFullURLRewrite() {
		if parsed, err := route.ParseFullURLRewrite(); err == nil {
			targetURL = *parsed
		}
	} else if route.Rewrite.Prefix != "" || route.HasRewriteRegex() {
		// Apply URL rewrite rules (prefix or regex), falling back to strip_prefix
		rewritten := route.RewritePath(r.URL.Path)
		targetURL.Path = singleJoiningSlash(target.Path, rewritten)
	} else if route.StripPrefix && route.PathPrefix {
		suffix := stripPrefix(route.Path, r.URL.Path)
		targetURL.Path = singleJoiningSlash(target.Path, suffix)
	} else {
		targetURL.Path = singleJoiningSlash(target.Path, r.URL.Path)
	}

	if !route.HasFullURLRewrite() {
		targetURL.RawQuery = r.URL.RawQuery
	}

	// Construct request directly — avoids URL.String() + url.Parse() round-trip.
	proxyReq := (&http.Request{
		Method:        r.Method,
		URL:           &targetURL,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Body:          r.Body,
		ContentLength: r.ContentLength,
		Host:          target.Host,
	}).WithContext(ctx)

	// Copy headers (+3 for X-Forwarded-For/Proto/Host added below)
	if header != nil {
		proxyReq.Header = header
	} else {
		proxyReq.Header = make(http.Header, len(r.Header)+3)
	}
	for k, vv := range r.Header {
		proxyReq.Header[k] = vv
	}

	// Set Host header (may be overridden below by rewrite config)
	proxyReq.Host = target.Host

	// Apply host override from rewrite config
	if route.Rewrite.Host != "" {
		proxyReq.Host = route.Rewrite.Host
	}

	// Set X-Forwarded headers via direct map access to bypass CanonicalMIMEHeaderKey.
	if clientIP := variables.ExtractClientIP(r); clientIP != "" {
		if prior, ok := proxyReq.Header["X-Forwarded-For"]; ok && len(prior) > 0 {
			proxyReq.Header["X-Forwarded-For"] = []string{prior[0] + ", " + clientIP}
		} else {
			proxyReq.Header["X-Forwarded-For"] = []string{clientIP}
		}
	}

	if r.TLS != nil {
		proxyReq.Header["X-Forwarded-Proto"] = xForwardedProtoHTTPS
	} else {
		proxyReq.Header["X-Forwarded-Proto"] = xForwardedProtoHTTP
	}

	proxyReq.Header["X-Forwarded-Host"] = []string{r.Host}

	// Remove hop-by-hop headers
	removeHopHeaders(proxyReq.Header)

	// Inject OTEL trace context + W3C baggage into outbound request
	if varCtx := variables.GetFromRequest(r); varCtx != nil && varCtx.PropagateTrace {
		otel.GetTextMapPropagator().Inject(proxyReq.Context(), propagation.HeaderCarrier(proxyReq.Header))
	}

	return proxyReq
}

// handleError handles proxy errors
func (p *Proxy) handleError(w http.ResponseWriter, r *http.Request, err error, backendURL string, balancer loadbalancer.Balancer) {
	// Backend health is managed by the active health checker and outlier
	// detector (when configured). Individual proxy errors should not
	// permanently eject backends — that causes cascading failures under load.

	if err == context.DeadlineExceeded {
		errors.ErrGatewayTimeout.WriteJSON(w)
		return
	}

	errors.ErrBadGateway.WithDetails(err.Error()).WriteJSON(w)
}

// copyHeaders copies headers from source to destination.
// Source slices are assigned directly instead of cloned — response headers
// from the backend are not mutated after copy, so sharing is safe.
func (p *Proxy) copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		dst[k] = vv
	}

	// Remove hop-by-hop headers from response
	removeHopHeaders(dst)
}

// copyBufPool avoids a 32KB allocation per io.Copy call on the proxy hot path.
var copyBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 32*1024)
		return &buf
	},
}

// copyBody copies the response body
func (p *Proxy) copyBody(w http.ResponseWriter, body io.Reader) {
	if p.flushInterval > 0 {
		// Streaming copy with flush
		if flusher, ok := w.(http.Flusher); ok {
			bp := copyBufPool.Get().(*[]byte)
			buf := *bp
			for {
				n, err := body.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
					flusher.Flush()
				}
				if err != nil {
					break
				}
			}
			copyBufPool.Put(bp)
			return
		}
	}

	bp := copyBufPool.Get().(*[]byte)
	io.CopyBuffer(w, body, *bp)
	copyBufPool.Put(bp)
}

// hopHeaders lists hop-by-hop headers that should be removed.
var hopHeaders = map[string]struct{}{
	"Connection":          {},
	"Proxy-Connection":    {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

func removeHopHeaders(header http.Header) {
	for h := range hopHeaders {
		delete(header, h)
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
	proxy              *Proxy
	balancer           loadbalancer.Balancer
	route              *router.Route
	transformer        *transform.PrecompiledTransform
	retryPolicy        *retry.Policy
	handler            http.Handler
	redirectTransport  *RedirectTransport // non-nil when follow_redirects is enabled
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

	// Create redirect transport if follow_redirects is enabled
	var transportOverride http.RoundTripper
	if route.FollowRedirects.Enabled {
		maxRedirects := route.FollowRedirects.MaxRedirects
		if maxRedirects == 0 {
			maxRedirects = 10
		}
		rt := NewRedirectTransport(proxy.transportPool.Get(route.UpstreamName), maxRedirects)
		rp.redirectTransport = rt
		transportOverride = rt
	}

	// Cache the handler, passing in the same retry policy so metrics are shared
	rp.handler = proxy.HandlerWithPolicy(route, rp.balancer, rp.retryPolicy, transportOverride)

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

	// Create redirect transport if follow_redirects is enabled
	var transportOverride http.RoundTripper
	if route.FollowRedirects.Enabled {
		maxRedirects := route.FollowRedirects.MaxRedirects
		if maxRedirects == 0 {
			maxRedirects = 10
		}
		rt := NewRedirectTransport(proxy.transportPool.Get(route.UpstreamName), maxRedirects)
		rp.redirectTransport = rt
		transportOverride = rt
	}

	// Cache the handler, passing in the same retry policy so metrics are shared
	rp.handler = proxy.HandlerWithPolicy(route, rp.balancer, rp.retryPolicy, transportOverride)

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

// GetRedirectTransport returns the redirect transport if follow_redirects is enabled.
func (rp *RouteProxy) GetRedirectTransport() *RedirectTransport {
	return rp.redirectTransport
}

// SetPerTryTimeout overrides the per-try timeout on the retry policy.
// This is safe because the handler closure captures the *retry.Policy pointer.
func (rp *RouteProxy) SetPerTryTimeout(d time.Duration) {
	if rp.retryPolicy != nil {
		rp.retryPolicy.PerTryTimeout = d
	}
}

// SetRetryBudget replaces the retry budget on this route's retry policy (for shared budget pools).
func (rp *RouteProxy) SetRetryBudget(b *retry.Budget) {
	if rp.retryPolicy != nil {
		rp.retryPolicy.SetBudget(b)
	}
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
		ID:        "simple",
		Path:      "/",
		Transform: config.TransformConfig{},
	}

	return proxy.Handler(route, balancer), nil
}
