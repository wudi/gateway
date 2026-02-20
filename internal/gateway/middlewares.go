package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/wudi/gateway/internal/cache"
	"github.com/wudi/gateway/internal/canary"
	"github.com/wudi/gateway/internal/circuitbreaker"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/errors"
	"github.com/wudi/gateway/internal/loadbalancer"
	"github.com/wudi/gateway/internal/metrics"
	"github.com/wudi/gateway/internal/middleware"
	"github.com/wudi/gateway/internal/middleware/geo"
	"github.com/wudi/gateway/internal/middleware/ipblocklist"
	"github.com/wudi/gateway/internal/middleware/ipfilter"
	openapivalidation "github.com/wudi/gateway/internal/middleware/openapi"
	"github.com/wudi/gateway/internal/middleware/transform"
	"github.com/wudi/gateway/internal/middleware/validation"
	grpcproxy "github.com/wudi/gateway/internal/proxy/grpc"
	"github.com/wudi/gateway/internal/bluegreen"
	"github.com/wudi/gateway/internal/router"
	"github.com/wudi/gateway/internal/rules"
	"github.com/wudi/gateway/internal/trafficshape"
	"github.com/wudi/gateway/internal/variables"
	"github.com/wudi/gateway/internal/websocket"
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

// 2.85 ipBlocklistMW checks global then per-route dynamic IP blocklists.
func ipBlocklistMW(global *ipblocklist.Blocklist, route *ipblocklist.Blocklist) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if global != nil {
				mw := global.Middleware()
				mw(http.HandlerFunc(func(w2 http.ResponseWriter, r2 *http.Request) {
					if route != nil {
						routeMW := route.Middleware()
						routeMW(next).ServeHTTP(w2, r2)
						return
					}
					next.ServeHTTP(w2, r2)
				})).ServeHTTP(w, r)
				return
			}
			if route != nil {
				routeMW := route.Middleware()
				routeMW(next).ServeHTTP(w, r)
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
			if evaluateRequestRules(global, w, r, varCtx) {
				return
			}
			if evaluateRequestRules(route, w, r, varCtx) {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// evaluateRequestRules runs request rules on the given engine and returns true if terminated.
func evaluateRequestRules(engine *rules.RuleEngine, w http.ResponseWriter, r *http.Request, varCtx *variables.Context) bool {
	if engine == nil || !engine.HasRequestRules() {
		return false
	}
	reqEnv := rules.NewRequestEnv(r, varCtx)
	for _, result := range engine.EvaluateRequest(reqEnv) {
		if result.Terminated {
			rules.ExecuteTerminatingAction(w, r, result.Action)
			return true
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
	return false
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
			shouldCache := h.ShouldCache(r)
			if shouldCache {
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

var errServerError = fmt.Errorf("server error")

// 10. circuitBreakerMW checks the circuit breaker and records outcomes.
func circuitBreakerMW(cb *circuitbreaker.Breaker, isGRPC bool) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			done, err := cb.Allow()
			if err != nil {
				errors.ErrServiceUnavailable.WithDetails("Circuit breaker is open").WriteJSON(w)
				return
			}

			rec := getStatusRecorder(w)
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
				done(errServerError)
			} else {
				done(nil)
			}
			putStatusRecorder(rec)
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
			rec := getStatusRecorder(w)
			next.ServeHTTP(rec, r)
			release(rec.statusCode, time.Since(start))
			putStatusRecorder(rec)
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

			evaluateResponseRules(global, rulesWriter, r, respEnv)
			evaluateResponseRules(route, rulesWriter, r, respEnv)

			rulesWriter.Flush()
		})
	}
}

// evaluateResponseRules runs response rules on the given engine.
func evaluateResponseRules(engine *rules.RuleEngine, rw *rules.RulesResponseWriter, r *http.Request, respEnv rules.ResponseEnv) {
	if engine == nil || !engine.HasResponseRules() {
		return
	}
	for _, result := range engine.EvaluateResponse(respEnv) {
		switch result.Action.Type {
		case "set_headers":
			rules.ExecuteResponseHeaders(rw, result.Action.Headers)
		case "log":
			rules.ExecuteResponseLog(result.RuleID, r, rw.StatusCode(), result.Action.LogMessage)
		}
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
	// Pre-compile header templates once — avoids per-request Resolve() parsing.
	pt := transform.NewPrecompiledTransform(route.Transform.Request.Headers)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)

			// Header transforms (pre-compiled)
			pt.ApplyToRequest(r, varCtx)

			// Body transforms
			if reqBodyTransform != nil {
				reqBodyTransform.TransformRequest(r, varCtx)
			}

			// gRPC preparation: deadline propagation, metadata transforms, size limits
			if grpcH != nil && grpcproxy.IsGRPCRequest(r) {
				var cancel func()
				r, cancel = grpcH.PrepareRequest(r)
				defer cancel()

				// Wrap response writer for send size limits + response metadata
				w = grpcH.WrapResponseWriter(w)

				defer grpcH.ProcessResponse(w)
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
			rec := getStatusRecorder(w)
			next.ServeHTTP(rec, r)
			mc.RecordRequest(routeID, r.Method, rec.statusCode, time.Since(start))
			putStatusRecorder(rec)
		})
	}
}

// varContextMW sets RouteID on the variable context.
// PathParams are already set by serveHTTP before the handler chain runs.
func varContextMW(routeID string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			varCtx.RouteID = routeID
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

// 22. canaryObserverMW records per-traffic-group outcomes for canary analysis.
func canaryObserverMW(ctrl *canary.Controller) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := getStatusRecorder(w)
			next.ServeHTTP(rec, r)

			varCtx := variables.GetFromRequest(r)
			if varCtx.TrafficGroup != "" {
				ctrl.RecordRequest(varCtx.TrafficGroup, rec.statusCode, time.Since(start))
			}
			putStatusRecorder(rec)
		})
	}
}

func blueGreenObserverMW(ctrl *bluegreen.Controller) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := getStatusRecorder(w)
			next.ServeHTTP(rec, r)

			varCtx := variables.GetFromRequest(r)
			if varCtx.TrafficGroup != "" {
				ctrl.RecordRequest(varCtx.TrafficGroup, rec.statusCode, time.Since(start))
			}
			putStatusRecorder(rec)
		})
	}
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

// isCollectionMW wraps JSON array responses as {"key": [...]} objects.
// This runs after backend encoding, before body transforms.
func isCollectionMW(collectionKey string) middleware.Middleware {
	if collectionKey == "" {
		collectionKey = "collection"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &collectionBufferWriter{
				ResponseWriter: w,
				header:         make(http.Header),
				statusCode:     200,
			}
			next.ServeHTTP(bw, r)

			body := bw.body.Bytes()

			// If JSON array response, wrap it
			if len(body) > 0 && body[0] == '[' {
				wrapped, err := json.Marshal(map[string]json.RawMessage{collectionKey: body})
				if err == nil {
					body = wrapped
				}
			}

			for k, vv := range bw.header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(bw.statusCode)
			w.Write(body)
		})
	}
}

type collectionBufferWriter struct {
	http.ResponseWriter
	statusCode int
	body       bytes.Buffer
	header     http.Header
}

func (w *collectionBufferWriter) Header() http.Header     { return w.header }
func (w *collectionBufferWriter) WriteHeader(code int)     { w.statusCode = code }
func (w *collectionBufferWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
