package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/example/gateway/internal/cache"
	"github.com/example/gateway/internal/circuitbreaker"
	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/example/gateway/internal/loadbalancer"
	"github.com/example/gateway/internal/metrics"
	"github.com/example/gateway/internal/middleware"
	"github.com/example/gateway/internal/middleware/compression"
	"github.com/example/gateway/internal/middleware/cors"
	"github.com/example/gateway/internal/middleware/ipfilter"
	"github.com/example/gateway/internal/middleware/ratelimit"
	"github.com/example/gateway/internal/middleware/transform"
	"github.com/example/gateway/internal/middleware/waf"
	"github.com/example/gateway/internal/middleware/validation"
	"github.com/example/gateway/internal/mirror"
	grpcproxy "github.com/example/gateway/internal/proxy/grpc"
	"github.com/example/gateway/internal/router"
	"github.com/example/gateway/internal/rules"
	"github.com/example/gateway/internal/trafficshape"
	"github.com/example/gateway/internal/variables"
	"github.com/example/gateway/internal/websocket"
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
					if result.Action.Type == "set_headers" {
						rules.ExecuteRequestHeaders(r, result.Action.Headers)
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
					if result.Action.Type == "set_headers" {
						rules.ExecuteRequestHeaders(r, result.Action.Headers)
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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if h.ShouldCache(r) {
				if entry, ok := h.Get(r); ok {
					mc.RecordCacheHit(routeID)
					cache.WriteCachedResponse(w, entry)
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
					h.Store(r, entry)
				}
				return
			}

			next.ServeHTTP(w, r)
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

// 11. compressionMW wraps the response writer with gzip compression.
func compressionMW(c *compression.Compressor) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !c.ShouldCompress(r) {
				next.ServeHTTP(w, r)
				return
			}
			cw := compression.NewCompressingResponseWriter(w, c)
			r.Header.Del("Accept-Encoding")
			next.ServeHTTP(cw, r)
			cw.Close()
		})
	}
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
					if result.Action.Type == "set_headers" {
						rules.ExecuteResponseHeaders(rulesWriter, result.Action.Headers)
					}
				}
			}
			if route != nil && route.HasResponseRules() {
				for _, result := range route.EvaluateResponse(respEnv) {
					if result.Action.Type == "set_headers" {
						rules.ExecuteResponseHeaders(rulesWriter, result.Action.Headers)
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
func requestTransformMW(route *router.Route, grpcH *grpcproxy.Handler) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)

			// Header transforms
			transformer := transform.NewHeaderTransformer()
			transformer.TransformRequest(r, route.Transform.Request.Headers, varCtx)

			// Body transforms
			bodyCfg := route.Transform.Request.Body
			if len(bodyCfg.AddFields) > 0 || len(bodyCfg.RemoveFields) > 0 || len(bodyCfg.RenameFields) > 0 {
				applyBodyTransform(r, bodyCfg)
			}

			// gRPC preparation
			if grpcH != nil && grpcproxy.IsGRPCRequest(r) {
				grpcH.PrepareRequest(r)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// 15. responseBodyTransformMW buffers the response, transforms the JSON body, and replays.
func responseBodyTransformMW(cfg config.BodyTransformConfig) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &mwBodyBufferWriter{
				ResponseWriter: w,
				statusCode:     200,
				header:         make(http.Header),
			}
			next.ServeHTTP(bw, r)

			transformed := mwApplyResponseBodyTransform(bw.body.Bytes(), cfg)
			// Copy captured headers to real writer
			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(transformed)))
			w.WriteHeader(bw.statusCode)
			w.Write(transformed)
		})
	}
}

// mwBodyBufferWriter captures the response for transformation.
type mwBodyBufferWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	header     http.Header
}

func (bw *mwBodyBufferWriter) Header() http.Header {
	return bw.header
}

func (bw *mwBodyBufferWriter) WriteHeader(code int) {
	bw.statusCode = code
}

func (bw *mwBodyBufferWriter) Write(b []byte) (int, error) {
	return bw.body.Write(b)
}

// mwApplyResponseBodyTransform applies response body transformations to JSON bodies.
func mwApplyResponseBodyTransform(body []byte, cfg config.BodyTransformConfig) []byte {
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return body
	}
	for key, value := range cfg.AddFields {
		data[key] = value
	}
	for _, key := range cfg.RemoveFields {
		delete(data, key)
	}
	for oldKey, newKey := range cfg.RenameFields {
		if val, ok := data[oldKey]; ok {
			data[newKey] = val
			delete(data, oldKey)
		}
	}
	newBody, err := json.Marshal(data)
	if err != nil {
		return body
	}
	return newBody
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

// routeMatchKey is the context key for storing the route match.
type routeMatchKey struct{}

// Ensure unused imports are satisfied.
var _ = io.Discard
