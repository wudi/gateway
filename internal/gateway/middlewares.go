package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wudi/gateway/internal/cache"
	"github.com/wudi/gateway/internal/canary"
	"github.com/wudi/gateway/internal/circuitbreaker"
	"github.com/wudi/gateway/internal/coalesce"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/loadbalancer/outlier"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/metrics"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/middleware/accesslog"
	"github.com/wudi/gateway/internal/middleware/backendauth"
	"github.com/wudi/gateway/internal/middleware/bodygen"
	"github.com/wudi/gateway/internal/middleware/botdetect"
	"github.com/wudi/gateway/internal/middleware/claimsprop"
	"github.com/wudi/gateway/internal/middleware/compression"
	"github.com/wudi/gateway/internal/middleware/contentneg"
	"github.com/wudi/gateway/internal/middleware/contentreplacer"
	"github.com/wudi/gateway/internal/middleware/cors"
	"github.com/wudi/gateway/internal/middleware/csrf"
	"github.com/wudi/gateway/internal/middleware/idempotency"
	"github.com/wudi/gateway/internal/middleware/errorpages"
	"github.com/wudi/gateway/internal/middleware/extauth"
	"github.com/wudi/gateway/internal/middleware/geo"
	"github.com/wudi/gateway/internal/middleware/ipfilter"
	"github.com/wudi/gateway/internal/middleware/mock"
	"github.com/wudi/gateway/internal/middleware/nonce"
	"github.com/wudi/gateway/internal/middleware/decompress"
	"github.com/wudi/gateway/internal/middleware/maintenance"
	"github.com/wudi/gateway/internal/middleware/paramforward"
	"github.com/wudi/gateway/internal/middleware/proxyratelimit"
	"github.com/wudi/gateway/internal/middleware/quota"
	"github.com/wudi/gateway/internal/middleware/respbodygen"
	"github.com/wudi/gateway/internal/middleware/securityheaders"
	"github.com/wudi/gateway/internal/middleware/signing"
	"github.com/wudi/gateway/internal/middleware/spikearrest"
	"github.com/wudi/gateway/internal/middleware/statusmap"
	openapivalidation "github.com/wudi/gateway/internal/middleware/openapi"
	"github.com/wudi/gateway/internal/middleware/ratelimit"
	"github.com/wudi/gateway/internal/middleware/responselimit"
	"github.com/wudi/gateway/internal/middleware/timeout"
	"github.com/wudi/gateway/internal/middleware/tokenrevoke"
	"github.com/wudi/gateway/internal/middleware/transform"
	"github.com/wudi/gateway/internal/middleware/validation"
	"github.com/wudi/gateway/internal/middleware/versioning"
	"github.com/wudi/gateway/internal/middleware/waf"
	"github.com/wudi/gateway/internal/mirror"
	grpcproxy "github.com/wudi/gateway/internal/proxy/grpc"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/internal/rules"
	"github.com/wudi/gateway/internal/trafficshape"
	"github.com/wudi/gateway/internal/variables"
	"github.com/wudi/gateway/internal/websocket"
	"go.uber.org/zap"
)

// 1. ipFilterMW checks global then per-route IP filters; rejects with 403.
func ipFilterMW(global *ipfilter.Filter, route *ipfilter.Filter) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if global != nil && !global.Check(r) {
				ipfilter.RejectRequest(w)
				return
			}
			if route != nil && !route.Check(r) {
				ipfilter.RejectRequest(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 1.5 geoMW checks global then per-route geo filters; rejects with 451.
func geoMW(global *geo.CompiledGeo, route *geo.CompiledGeo) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if global != nil {
				var allowed bool
				r, allowed = global.Handle(w, r)
				if !allowed {
					return
				}
			}
			if route != nil {
				var allowed bool
				r, allowed = route.Handle(w, r)
				if !allowed {
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 2. corsMW handles CORS preflight and applies response headers.
func corsMW(h *cors.Handler) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h.IsPreflight(r) {
				h.HandlePreflight(w, r)
				return
			}
			h.ApplyHeaders(w, r)
			next.ServeHTTP(w, r)
		})
	}
}

// 3. authMW authenticates requests using the gateway's auth providers.
func authMW(g *Gateway, cfg router.RouteAuth) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !g.authenticate(w, r, cfg.Methods) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 3.5. extAuthMW calls an external auth service before allowing the request.
func extAuthMW(ea *extauth.ExtAuth) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			result, err := ea.Check(r)
			if err != nil {
				errors.ErrBadGateway.WriteJSON(w)
				return
			}
			if !result.Allowed {
				// Copy denied headers
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
			// Inject headers from auth service into upstream request
			for k, v := range result.HeadersToInject {
				r.Header.Set(k, v)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 3.7. nonceMW checks nonce for replay prevention.
func nonceMW(nc *nonce.NonceChecker) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, statusCode, msg := nc.Check(r)
			if !allowed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				fmt.Fprintf(w, `{"error":"%s"}`, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 3.8. csrfMW validates CSRF double-submit cookie tokens.
func csrfMW(cp *csrf.CompiledCSRF) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			allowed, statusCode, msg := cp.Check(w, r)
			if !allowed {
				http.Error(w, msg, statusCode)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 3.9. idempotencyMW checks idempotency keys and replays cached responses.
func idempotencyMW(ci *idempotency.CompiledIdempotency) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			outcome := ci.Check(r)
			switch outcome.Result {
			case idempotency.ResultCached, idempotency.ResultWaited:
				idempotency.ReplayResponse(w, outcome.Response)
				return
			case idempotency.ResultReject:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				fmt.Fprintf(w, `{"error":"Idempotency-Key header is required for this request"}`)
				return
			case idempotency.ResultInvalid:
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"error":"Idempotency-Key is too long"}`)
				return
			}

			// ResultProceed — wrap writer to capture response
			if outcome.Key != "" {
				cw := idempotency.NewCapturingWriter(w)
				defer func() {
					ci.RecordResponse(outcome.Key, cw.ToStoredResponse())
				}()
				next.ServeHTTP(cw, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// 4. requestRulesMW evaluates global then per-route request rules.
func requestRulesMW(global, route *rules.RuleEngine) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)

			if global != nil && global.HasRequestRules() {
				reqEnv := rules.NewRequestEnv(r, varCtx)
				for _, result := range global.EvaluateRequest(reqEnv) {
					if result.Terminated {
						rules.ExecuteTerminatingAction(w, r, result.Action)
						return
					}
					switch result.Action.Type {
					case "set_headers":
						rules.ExecuteRequestHeaders(r, result.Action.Headers)
					case "rewrite":
						rules.ExecuteRewrite(r, result.Action.Rewrite)
					case "group":
						rules.ExecuteGroup(varCtx, result.Action.Group)
					case "log":
						rules.ExecuteLog(result.RuleID, r, varCtx, result.Action.LogMessage)
					}
				}
			}

			if route != nil && route.HasRequestRules() {
				reqEnv := rules.NewRequestEnv(r, varCtx)
				for _, result := range route.EvaluateRequest(reqEnv) {
					if result.Terminated {
						rules.ExecuteTerminatingAction(w, r, result.Action)
						return
					}
					switch result.Action.Type {
					case "set_headers":
						rules.ExecuteRequestHeaders(r, result.Action.Headers)
					case "rewrite":
						rules.ExecuteRewrite(r, result.Action.Rewrite)
					case "group":
						rules.ExecuteGroup(varCtx, result.Action.Group)
					case "log":
						rules.ExecuteLog(result.RuleID, r, varCtx, result.Action.LogMessage)
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// 5. rateLimitMW applies per-route rate limiting.
func rateLimitMW(l *ratelimit.Limiter) middleware.Middleware {
	return l.Middleware()
}

// 6. bodyLimitMW enforces a request body size limit.
func bodyLimitMW(max int64) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > max {
				errors.ErrRequestEntityTooLarge.WithDetails(
					fmt.Sprintf("Request body exceeds maximum size of %d bytes", max),
				).WriteJSON(w)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, max)
			next.ServeHTTP(w, r)
		})
	}
}

// 6.5. requestDecompressMW decompresses request bodies with Content-Encoding.
func requestDecompressMW(d *decompress.Decompressor) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if algo, ok := d.ShouldDecompress(r); ok {
				if err := d.Decompress(r, algo); err != nil {
					http.Error(w, `{"error":"request decompression failed"}`, http.StatusBadRequest)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 7. validationMW validates the request body against a schema.
func validationMW(v *validation.Validator) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := v.Validate(r); err != nil {
				validation.RejectValidation(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 8. websocketMW upgrades WebSocket requests; non-WS requests pass through.
func websocketMW(wsProxy *websocket.Proxy, getBalancer func() loadbalancer.Balancer) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if websocket.IsUpgradeRequest(r) {
				backend := getBalancer().Next()
				if backend == nil {
					errors.ErrServiceUnavailable.WithDetails("No healthy backends available").WriteJSON(w)
					return
				}
				wsProxy.ServeHTTP(w, r, backend.URL)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 9. cacheMW handles both cache HIT (early return) and MISS (wrap writer, store after proxy).
func cacheMW(h *cache.Handler, mc *metrics.Collector, routeID string) middleware.Middleware {
	conditional := h.IsConditional()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h.ShouldCache(r) {
				if entry, ok := h.Get(r); ok {
					mc.RecordCacheHit(routeID)
					notModified := cache.WriteCachedResponse(w, r, entry, conditional)
					if notModified {
						h.RecordNotModified()
						mc.RecordCacheNotModified(routeID)
					}
					return
				}
				mc.RecordCacheMiss(routeID)
			}

			// Invalidate cache on mutating requests
			if cache.IsMutatingMethod(r.Method) {
				h.InvalidateByPath(r.URL.Path)
			}

			// Wrap writer for cache capture on cacheable requests
			shouldCache := h.ShouldCache(r)
			if shouldCache {
				cachingWriter := cache.NewCachingResponseWriter(w)
				cachingWriter.Header().Set("X-Cache", "MISS")
				next.ServeHTTP(cachingWriter, r)

				// Store if response is cacheable
				if h.ShouldStore(cachingWriter.StatusCode(), cachingWriter.Header(), int64(cachingWriter.Body.Len())) {
					entry := &cache.Entry{
						StatusCode: cachingWriter.StatusCode(),
						Headers:    cachingWriter.Header().Clone(),
						Body:       cachingWriter.Body.Bytes(),
					}
					if conditional {
						cache.PopulateConditionalFields(entry)
					}
					h.Store(r, entry)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// 9.5. coalesceMW deduplicates concurrent identical requests via singleflight.
func coalesceMW(c *coalesce.Coalescer) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !c.ShouldCoalesce(r) {
				next.ServeHTTP(w, r)
				return
			}
			c.ServeCoalesced(w, r, next)
		})
	}
}

// 10. circuitBreakerMW checks the circuit breaker and records outcomes.
func circuitBreakerMW(cb *circuitbreaker.Breaker, isGRPC bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			done, err := cb.Allow()
			if err != nil {
				errors.ErrServiceUnavailable.WithDetails("Circuit breaker is open").WriteJSON(w)
				return
			}

			rec := &statusRecorder{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(rec, r)

			// Report outcome
			cbStatus := rec.statusCode
			if isGRPC && rec.statusCode == 200 {
				grpcStatus := w.Header().Get("Grpc-Status")
				if grpcStatus != "" && grpcStatus != "0" {
					cbStatus = 500
				}
			}
			if cbStatus >= 500 {
				done(fmt.Errorf("server error: %d", cbStatus))
			} else {
				done(nil)
			}
		})
	}
}

// 10.5. adaptiveConcurrencyMW enforces adaptive concurrency limits.
func adaptiveConcurrencyMW(al *trafficshape.AdaptiveLimiter) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			release, ok := al.Allow()
			if !ok {
				errors.ErrServiceUnavailable.WithDetails("Adaptive concurrency limit reached").WriteJSON(w)
				return
			}
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(rec, r)
			release(rec.statusCode, time.Since(start))
		})
	}
}

// 11. compressionMW wraps the response writer with negotiated compression.
func compressionMW(c *compression.Compressor) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			algo := c.NegotiateEncoding(r)
			if algo == "" {
				next.ServeHTTP(w, r)
				return
			}
			cw := compression.NewCompressingResponseWriter(w, c, algo)
			r.Header.Del("Accept-Encoding")
			next.ServeHTTP(cw, r)
			cw.Close()
		})
	}
}

// 11.5. responseLimitMW enforces a maximum response body size.
func responseLimitMW(rl *responselimit.ResponseLimiter) middleware.Middleware {
	return rl.Middleware()
}

// 12. responseRulesMW wraps with RulesResponseWriter, evaluates response rules, then flushes.
func responseRulesMW(global, route *rules.RuleEngine) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rulesWriter := rules.NewRulesResponseWriter(w)
			next.ServeHTTP(rulesWriter, r)

			varCtx := variables.GetFromRequest(r)
			respEnv := rules.NewResponseEnv(r, varCtx, rulesWriter.StatusCode(), rulesWriter.Header())

			if global != nil && global.HasResponseRules() {
				for _, result := range global.EvaluateResponse(respEnv) {
					switch result.Action.Type {
					case "set_headers":
						rules.ExecuteResponseHeaders(rulesWriter, result.Action.Headers)
					case "log":
						rules.ExecuteResponseLog(result.RuleID, r, rulesWriter.StatusCode(), result.Action.LogMessage)
					}
				}
			}
			if route != nil && route.HasResponseRules() {
				for _, result := range route.EvaluateResponse(respEnv) {
					switch result.Action.Type {
					case "set_headers":
						rules.ExecuteResponseHeaders(rulesWriter, result.Action.Headers)
					case "log":
						rules.ExecuteResponseLog(result.RuleID, r, rulesWriter.StatusCode(), result.Action.LogMessage)
					}
				}
			}

			rulesWriter.Flush()
		})
	}
}

// 13. mirrorMW buffers the request body and sends mirrored requests async.
// If compare is enabled, wraps writer with CapturingWriter to capture primary response.
func mirrorMW(m *mirror.Mirror) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !m.ShouldMirror(r) {
				next.ServeHTTP(w, r)
				return
			}

			mirrorBody, err := mirror.BufferRequestBody(r)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			if m.CompareEnabled() {
				cw := mirror.NewCapturingWriter(w)
				next.ServeHTTP(cw, r)
				primary := &mirror.PrimaryResponse{
					StatusCode: cw.StatusCode(),
					BodyHash:   cw.BodyHash(),
				}
				m.SendAsync(r, mirrorBody, primary)
			} else {
				next.ServeHTTP(w, r)
				m.SendAsync(r, mirrorBody, nil)
			}
		})
	}
}

// trafficGroupMW injects A/B variant response header and sticky cookie after proxy completes.
func trafficGroupMW(sp *loadbalancer.StickyPolicy) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			abw := &abVariantWriter{ResponseWriter: w, sp: sp, r: r}
			next.ServeHTTP(abw, r)
		})
	}
}

// abVariantWriter intercepts WriteHeader to inject traffic group headers and cookies.
type abVariantWriter struct {
	http.ResponseWriter
	sp            *loadbalancer.StickyPolicy
	r             *http.Request
	headerWritten bool
}

func (w *abVariantWriter) WriteHeader(code int) {
	if !w.headerWritten {
		w.headerWritten = true
		varCtx := variables.GetFromRequest(w.r)
		if varCtx.TrafficGroup != "" {
			w.ResponseWriter.Header().Set("X-AB-Variant", varCtx.TrafficGroup)
			if cookie := w.sp.SetCookie(varCtx.TrafficGroup); cookie != nil {
				http.SetCookie(w.ResponseWriter, cookie)
			}
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *abVariantWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}

func (w *abVariantWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// 14. requestTransformMW applies header/body transformations and gRPC preparation.
func requestTransformMW(route *router.Route, grpcH *grpcproxy.Handler, reqBodyTransform *transform.CompiledBodyTransform) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)

			// Header transforms
			transformer := transform.NewHeaderTransformer()
			transformer.TransformRequest(r, route.Transform.Request.Headers, varCtx)

			// Body transforms
			if reqBodyTransform != nil {
				reqBodyTransform.TransformRequest(r, varCtx)
			}

			// gRPC preparation
			if grpcH != nil && grpcproxy.IsGRPCRequest(r) {
				grpcH.PrepareRequest(r)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// 16. metricsMW records request metrics (timing + status).
func metricsMW(mc *metrics.Collector, routeID string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(rec, r)
			mc.RecordRequest(routeID, r.Method, rec.statusCode, time.Since(start))
		})
	}
}

// varContextMW sets RouteID and PathParams on the variable context.
func varContextMW(routeID string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			match := r.Context().Value(routeMatchKey{}).(*router.Match)
			varCtx := variables.GetFromRequest(r)
			varCtx.RouteID = routeID
			varCtx.PathParams = match.PathParams
			ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, varCtx)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// maintenanceMW checks if the route is in maintenance mode and short-circuits with a response.
func maintenanceMW(cm *maintenance.CompiledMaintenance) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cm.ShouldBlock(r) {
				cm.WriteResponse(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeadersMW injects configured security response headers.
func securityHeadersMW(sh *securityheaders.CompiledSecurityHeaders) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sh.Apply(w.Header())
			next.ServeHTTP(w, r)
		})
	}
}

// errorPagesMW intercepts error responses and renders custom error pages.
func errorPagesMW(ep *errorpages.CompiledErrorPages) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			epw := &errorPageWriter{
				ResponseWriter: w,
				ep:             ep,
				r:              r,
			}
			next.ServeHTTP(epw, r)
		})
	}
}

// errorPageWriter intercepts WriteHeader to render custom error pages for error status codes.
type errorPageWriter struct {
	http.ResponseWriter
	ep          *errorpages.CompiledErrorPages
	r           *http.Request
	intercepted bool
	wroteHeader bool
}

func (w *errorPageWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	if code >= 400 && w.ep.ShouldIntercept(code) {
		w.intercepted = true
		varCtx := variables.GetFromRequest(w.r)
		body, contentType := w.ep.Render(code, w.r, varCtx)

		// Clear any existing content headers before writing custom error page
		w.ResponseWriter.Header().Del("Content-Encoding")
		w.ResponseWriter.Header().Set("Content-Type", contentType)
		w.ResponseWriter.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.ResponseWriter.WriteHeader(code)
		w.ResponseWriter.Write([]byte(body))
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *errorPageWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.intercepted {
		// Silently discard — custom body already written
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *errorPageWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// 17. throttleMW delays requests using the throttler's token bucket.
func throttleMW(t *trafficshape.Throttler) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := t.Throttle(r.Context(), r); err != nil {
				errors.ErrServiceUnavailable.WithDetails("Request throttled: queue timeout exceeded").WriteJSON(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 18. priorityMW enforces priority-based admission control.
func priorityMW(admitter *trafficshape.PriorityAdmitter, cfg config.PriorityConfig) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			level := trafficshape.DetermineLevel(r, varCtx.Identity, cfg)

			ctx := r.Context()
			if cfg.MaxWait > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, cfg.MaxWait)
				defer cancel()
			}

			release, err := admitter.Admit(ctx, level)
			if err != nil {
				errors.ErrServiceUnavailable.WithDetails("Priority admission timeout").WriteJSON(w)
				return
			}
			defer release()

			next.ServeHTTP(w, r)
		})
	}
}

// 19. faultInjectionMW injects delays and/or aborts for chaos testing.
func faultInjectionMW(fi *trafficshape.FaultInjector) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			aborted, statusCode := fi.Apply(r.Context())
			if aborted {
				w.WriteHeader(statusCode)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// 21. wafMW runs WAF inspection on the request.
func wafMW(w *waf.WAF) middleware.Middleware {
	return w.Middleware()
}

// 20. bandwidthMW wraps request body and response writer with bandwidth limits.
func bandwidthMW(bw *trafficshape.BandwidthLimiter) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw.WrapRequest(r)
			wrappedW := bw.WrapResponse(w)
			next.ServeHTTP(wrappedW, r)
		})
	}
}

// 22. canaryObserverMW records per-traffic-group outcomes for canary analysis.
func canaryObserverMW(ctrl *canary.Controller) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, statusCode: 200}
			next.ServeHTTP(rec, r)

			varCtx := variables.GetFromRequest(r)
			if varCtx.TrafficGroup != "" {
				ctrl.RecordRequest(varCtx.TrafficGroup, rec.statusCode, time.Since(start))
			}
		})
	}
}

// versioningMW detects the API version, sets it in context, strips prefix if configured, and injects deprecation headers.
func versioningMW(v *versioning.Versioner) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			version := v.DetectVersion(r)
			varCtx := variables.GetFromRequest(r)
			varCtx.APIVersion = version
			v.StripVersionPrefix(r, version)
			v.InjectDeprecationHeaders(w, version)
			next.ServeHTTP(w, r)
		})
	}
}

// accessLogMW stores the per-route access log config on the variable context and
// optionally captures request/response bodies for the global logging middleware.
func accessLogMW(cfg *accesslog.CompiledAccessLog) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			varCtx.AccessLogConfig = cfg

			// Capture request body if configured
			if cfg.Body.Enabled && cfg.Body.Request {
				if cfg.ShouldCaptureBody(r.Header.Get("Content-Type")) {
					body, err := io.ReadAll(io.LimitReader(r.Body, int64(cfg.Body.MaxSize)+1))
					if err == nil {
						truncated := len(body) > cfg.Body.MaxSize
						if truncated {
							body = body[:cfg.Body.MaxSize]
						}
						varCtx.Custom["_al_req_body"] = string(body)
						// Replay body
						r.Body = io.NopCloser(io.MultiReader(
							strings.NewReader(string(body)),
							r.Body,
						))
					}
				}
			}

			// Capture response body if configured
			if cfg.Body.Enabled && cfg.Body.Response {
				bcw := accesslog.NewBodyCapturingWriter(w, cfg.Body.MaxSize)
				next.ServeHTTP(bcw, r)
				// Check content type after proxy wrote response
				if cfg.ShouldCaptureBody(bcw.Header().Get("Content-Type")) {
					varCtx.Custom["_al_resp_body"] = bcw.CapturedBody()
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// timeoutMW applies a request-level context deadline and injects Retry-After on 504.
func timeoutMW(ct *timeout.CompiledTimeout) middleware.Middleware {
	return ct.Middleware()
}

// openapiRequestMW validates requests against an OpenAPI spec.
func openapiRequestMW(ov *openapivalidation.CompiledOpenAPI) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			if err := ov.ValidateRequest(r, varCtx.PathParams); err != nil {
				if ov.IsLogOnly() {
					// Log and continue
					next.ServeHTTP(w, r)
					return
				}
				validation.RejectValidation(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// responseValidationMW validates backend responses against standalone and/or OpenAPI schemas.
// It buffers the response body (up to maxValidationBody) and validates before sending to client.
func responseValidationMW(v *validation.Validator, ov *openapivalidation.CompiledOpenAPI) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vw := &validatingResponseWriter{
				ResponseWriter: w,
				maxSize:        1 << 20, // 1MB
			}
			next.ServeHTTP(vw, r)

			// If body exceeded maxSize, it was already streamed through — skip validation
			if vw.overflowed {
				return
			}

			body := vw.buf.Bytes()
			logOnly := false
			var validationErr error

			// Standalone JSON Schema response validation
			if v != nil && v.HasResponseSchema() {
				if v.IsLogOnly() {
					logOnly = true
				}
				if err := v.ValidateResponseBody(body); err != nil {
					validationErr = err
				}
			}

			// OpenAPI response validation
			if validationErr == nil && ov != nil && ov.ValidatesResponse() {
				if ov.IsLogOnly() {
					logOnly = true
				}
				varCtx := variables.GetFromRequest(r)
				if err := ov.ValidateResponse(vw.statusCode, vw.header, io.NopCloser(bytes.NewReader(body)), r, varCtx.PathParams); err != nil {
					validationErr = err
				}
			}

			if validationErr != nil && !logOnly {
				// Invalid response: return 502
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadGateway)
				w.Write([]byte(`{"error":"bad_gateway","message":"Backend response validation failed"}`))
				return
			}

			// Valid (or logOnly): flush buffered response
			vw.flush()
		})
	}
}

// validatingResponseWriter buffers the response up to maxSize.
// If the body exceeds maxSize, it switches to pass-through mode.
type validatingResponseWriter struct {
	http.ResponseWriter
	maxSize     int
	buf         bytes.Buffer
	statusCode  int
	header      http.Header
	overflowed  bool
	wroteHeader bool
}

func (w *validatingResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
	// Clone headers so we can replay them later
	w.header = w.ResponseWriter.Header().Clone()
}

func (w *validatingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(200)
	}
	if w.overflowed {
		return w.ResponseWriter.Write(b)
	}
	if w.buf.Len()+len(b) > w.maxSize {
		// Switch to pass-through: flush what we have and stream the rest
		w.overflowed = true
		for k, vv := range w.header {
			for _, v := range vv {
				w.ResponseWriter.Header().Add(k, v)
			}
		}
		w.ResponseWriter.WriteHeader(w.statusCode)
		w.ResponseWriter.Write(w.buf.Bytes())
		w.buf.Reset()
		return w.ResponseWriter.Write(b)
	}
	return w.buf.Write(b)
}

func (w *validatingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// flush writes the buffered status, headers, and body to the underlying writer.
func (w *validatingResponseWriter) flush() {
	for k, vv := range w.header {
		for _, v := range vv {
			w.ResponseWriter.Header().Add(k, v)
		}
	}
	if w.statusCode == 0 {
		w.statusCode = 200
	}
	w.ResponseWriter.WriteHeader(w.statusCode)
	w.ResponseWriter.Write(w.buf.Bytes())
}

// outlierDetectionMW records per-backend request outcomes for outlier detection.
func outlierDetectionMW(det *outlier.Detector) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			varCtx := variables.GetFromRequest(r)
			if varCtx.UpstreamAddr != "" {
				status := varCtx.UpstreamStatus
				if status == 0 {
					status = 502 // connection error
				}
				det.Record(varCtx.UpstreamAddr, status, varCtx.UpstreamResponseTime)
			}
		})
	}
}

// backendSigningMW signs outgoing requests with HMAC before they reach the backend.
func backendSigningMW(signer *signing.CompiledSigner) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := signer.Sign(r); err != nil {
				logging.Warn("backend signing failed",
					zap.String("route_id", signer.RouteID()),
					zap.Error(err),
				)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// botDetectMW blocks requests from denied User-Agent patterns.
func botDetectMW(bd *botdetect.BotDetector) middleware.Middleware {
	return bd.Middleware()
}

// proxyRateLimitMW limits outbound requests per route to protect backends.
func proxyRateLimitMW(pl *proxyratelimit.ProxyLimiter) middleware.Middleware {
	return pl.Middleware()
}

// mockMW returns a static response without calling the backend.
func mockMW(mh *mock.MockHandler) middleware.Middleware {
	return mh.Middleware()
}

// claimsPropMW propagates JWT claims as request headers to backends.
func claimsPropMW(cp *claimsprop.ClaimsPropagator) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cp.Apply(r)
			next.ServeHTTP(w, r)
		})
	}
}

// tokenRevokeMW rejects requests with revoked JWT tokens.
func tokenRevokeMW(tc *tokenrevoke.TokenChecker) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !tc.Check(r) {
				errors.New(http.StatusUnauthorized, "Token has been revoked").WriteJSON(w)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// backendAuthMW injects an OAuth2 access token into backend requests.
func backendAuthMW(ba *backendauth.TokenProvider) middleware.Middleware {
	return ba.Middleware()
}

// statusMapMW remaps backend response status codes.
func statusMapMW(sm *statusmap.StatusMapper) middleware.Middleware {
	return sm.Middleware()
}

// spikeArrestMW enforces continuous rate limiting with immediate rejection.
func spikeArrestMW(sa *spikearrest.SpikeArrester) middleware.Middleware {
	return sa.Middleware()
}

// contentReplacerMW applies regex replacements to response body/headers.
func contentReplacerMW(cr *contentreplacer.ContentReplacer) middleware.Middleware {
	return cr.Middleware()
}

// bodyGenMW generates request body from a Go template.
func bodyGenMW(bg *bodygen.BodyGen) middleware.Middleware {
	return bg.Middleware()
}

// quotaMW enforces per-client usage quotas over billing periods.
func quotaMW(qe *quota.QuotaEnforcer) middleware.Middleware {
	return qe.Middleware()
}

// respBodyGenMW generates response body from a Go template.
func respBodyGenMW(rbg *respbodygen.RespBodyGen) middleware.Middleware {
	return rbg.Middleware()
}

// paramForwardMW strips disallowed headers/query/cookies.
func paramForwardMW(pf *paramforward.ParamForwarder) middleware.Middleware {
	return pf.Middleware()
}

// contentNegMW re-encodes response based on Accept header.
func contentNegMW(cn *contentneg.Negotiator) middleware.Middleware {
	return cn.Middleware()
}

// routeMatchKey is the context key for storing the route match.
type routeMatchKey struct{}

// Ensure unused imports are satisfied.
var _ = io.Discard
