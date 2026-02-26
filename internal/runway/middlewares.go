package runway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/wudi/runway/internal/cache"
	"github.com/wudi/runway/internal/canary"
	"github.com/wudi/runway/internal/circuitbreaker"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/errors"
	"github.com/wudi/runway/internal/logging"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/internal/metrics"
	"github.com/wudi/runway/internal/middleware"
	"github.com/wudi/runway/internal/middleware/geo"
	"github.com/wudi/runway/internal/middleware/ipblocklist"
	"github.com/wudi/runway/internal/middleware/ipfilter"
	openapivalidation "github.com/wudi/runway/internal/middleware/openapi"
	"github.com/wudi/runway/internal/middleware/tenant"
	"github.com/wudi/runway/internal/middleware/transform"
	"github.com/wudi/runway/internal/middleware/validation"
	grpcproxy "github.com/wudi/runway/internal/proxy/grpc"
	"github.com/wudi/runway/internal/abtest"
	"github.com/wudi/runway/internal/bluegreen"
	"github.com/wudi/runway/internal/router"
	"github.com/wudi/runway/internal/rules"
	"github.com/wudi/runway/internal/trafficshape"
	"github.com/wudi/runway/variables"
	"github.com/wudi/runway/internal/websocket"
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
func authMW(g *Runway, cfg router.RouteAuth) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if variables.GetFromRequest(r).SkipFlags&variables.SkipAuth != 0 {
				next.ServeHTTP(w, r)
				return
			}
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
			var terminated bool
			r, terminated = evaluateRequestRules(global, w, r, varCtx)
			if terminated {
				return
			}
			r, terminated = evaluateRequestRules(route, w, r, varCtx)
			if terminated {
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// evaluateRequestRules runs request rules on the given engine.
// Returns the (possibly modified) request and true if a terminating action fired.
func evaluateRequestRules(engine *rules.RuleEngine, w http.ResponseWriter, r *http.Request, varCtx *variables.Context) (*http.Request, bool) {
	if engine == nil || !engine.HasRequestRules() {
		return r, false
	}
	reqEnv := rules.NewRequestEnv(r, varCtx)
	for _, result := range engine.EvaluateRequest(reqEnv) {
		if result.Terminated {
			rules.ExecuteTerminatingAction(w, r, result.Action)
			return r, true
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
		case "delay":
			rules.ExecuteDelay(result.Action.Delay)
		case "set_var":
			rules.ExecuteSetVar(varCtx, result.Action.Variables)
		case "cache_bypass":
			r = rules.SetCacheBypass(r)
		case "lua":
			if err := rules.ExecuteLuaRequest(engine.LuaPool(), result.Action.LuaProto, r, varCtx); err != nil {
				logging.Error("lua rule action error",
					zap.String("rule_id", result.RuleID),
					zap.Error(err),
				)
			}
		case "skip_auth", "skip_rate_limit", "skip_throttle", "skip_circuit_breaker",
			"skip_waf", "skip_validation", "skip_compression", "skip_adaptive_concurrency",
			"skip_body_limit", "skip_mirror", "skip_access_log", "skip_cache_store":
			rules.ExecuteSkip(varCtx, skipFlagMap[result.Action.Type])
		case "rate_limit_tier":
			rules.ExecuteRateLimitTier(varCtx, result.Action.Tier)
		case "timeout_override":
			rules.ExecuteTimeoutOverride(varCtx, result.Action.Timeout)
		case "priority_override":
			rules.ExecutePriorityOverride(varCtx, result.Action.Priority)
		case "bandwidth_override":
			rules.ExecuteBandwidthOverride(varCtx, result.Action.Bandwidth)
		case "body_limit_override":
			rules.ExecuteBodyLimitOverride(varCtx, result.Action.BodyLimit)
		case "switch_backend":
			rules.ExecuteSwitchBackend(varCtx, result.Action.Backend)
		}
	}
	return r, false
}

// skipFlagMap maps action type strings to SkipFlags constants.
var skipFlagMap = map[string]variables.SkipFlags{
	"skip_auth":                 variables.SkipAuth,
	"skip_rate_limit":           variables.SkipRateLimit,
	"skip_throttle":             variables.SkipThrottle,
	"skip_circuit_breaker":      variables.SkipCircuitBreaker,
	"skip_waf":                  variables.SkipWAF,
	"skip_validation":           variables.SkipValidation,
	"skip_compression":          variables.SkipCompression,
	"skip_adaptive_concurrency": variables.SkipAdaptiveConcurrency,
	"skip_body_limit":           variables.SkipBodyLimit,
	"skip_mirror":               variables.SkipMirror,
	"skip_access_log":           variables.SkipAccessLog,
	"skip_cache_store":          variables.SkipCacheStore,
}

// 6. bodyLimitMW enforces a request body size limit.
// If the resolved tenant has a MaxBodySize configured, the effective limit is
// min(routeMax, tenantMax) so that tenants cannot exceed their allocation.
func bodyLimitMW(max int64) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			varCtx := variables.GetFromRequest(r)
			if varCtx.SkipFlags&variables.SkipBodyLimit != 0 {
				next.ServeHTTP(w, r)
				return
			}
			limit := max
			if varCtx.Overrides != nil && varCtx.Overrides.BodyLimitOverride > 0 {
				limit = varCtx.Overrides.BodyLimitOverride
			}
			if ti := tenant.FromContext(r.Context()); ti != nil && ti.Config.MaxBodySize > 0 {
				if ti.Config.MaxBodySize < limit {
					limit = ti.Config.MaxBodySize
				}
			}
			if r.ContentLength > limit {
				errors.ErrRequestEntityTooLarge.WithDetails(
					fmt.Sprintf("Request body exceeds maximum size of %d bytes", limit),
				).WriteJSON(w)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
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
// Supports stale-while-revalidate (serve stale + background refresh) and
// stale-if-error (serve stale when backend returns 5xx).
func cacheMW(h *cache.Handler, mc *metrics.Collector, routeID string) middleware.Middleware {
	conditional := h.IsConditional()
	hasStale := h.HasStaleSupport()
	swr := h.StaleWhileRevalidate()
	sie := h.StaleIfError()

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			shouldCache := h.ShouldCache(r) && !rules.IsCacheBypass(r)
			if shouldCache {
				if hasStale {
					key := h.KeyForRequest(r)
					entry, fresh, stale := h.GetWithStaleness(key)

					if entry != nil && fresh {
						// Fresh cache hit — serve as normal
						mc.RecordCacheHit(routeID)
						notModified := cache.WriteCachedResponse(w, r, entry, conditional)
						if notModified {
							h.RecordNotModified()
							mc.RecordCacheNotModified(routeID)
						}
						return
					}

					if entry != nil && stale {
						age := time.Since(entry.StoredAt)

						// stale-while-revalidate: serve stale immediately, revalidate in background
						if swr > 0 && age <= h.TTL()+swr {
							mc.RecordCacheHit(routeID)
							writeStaleResponse(w, r, entry, conditional)

							// Trigger background revalidation (deduped by key)
							if !h.IsRevalidating(key) {
								go func() {
									defer h.DoneRevalidating(key)
									revalidateInBackground(h, next, r, key, conditional)
								}()
							}
							return
						}

						// Entry is stale but within stale-if-error window — proceed
						// to backend but fall back to stale if backend fails.
						if sie > 0 && age <= h.TTL()+sie {
							capWriter := cache.NewCapturingResponseWriter()
							next.ServeHTTP(capWriter, r)

							if capWriter.StatusCode() >= 500 {
								// Backend error — serve stale entry instead
								mc.RecordCacheHit(routeID)
								writeStaleResponse(w, r, entry, conditional)
								return
							}

							// Backend succeeded — write response to client
							for k, vv := range capWriter.Header() {
								for _, v := range vv {
									w.Header().Add(k, v)
								}
							}
							w.Header().Set("X-Cache", "MISS")
							w.WriteHeader(capWriter.StatusCode())
							w.Write(capWriter.Body.Bytes())

							// Store if response is cacheable
							sieCtx := variables.GetFromRequest(r)
							if sieCtx.SkipFlags&variables.SkipCacheStore == 0 &&
								h.ShouldStore(capWriter.StatusCode(), capWriter.Header(), int64(capWriter.Body.Len())) {
								newEntry := &cache.Entry{
									StatusCode: capWriter.StatusCode(),
									Headers:    capWriter.Header().Clone(),
									Body:       capWriter.Body.Bytes(),
								}
								if conditional {
									cache.PopulateConditionalFields(newEntry)
								}
								if sieCtx.Overrides != nil && sieCtx.Overrides.CacheTTLOverride > 0 {
									newEntry.TTL = sieCtx.Overrides.CacheTTLOverride
								}
								h.StoreWithMeta(key, r.URL.Path, newEntry)
							}
							return
						}
					}

					// No usable entry — fall through to normal miss path
				} else {
					// No stale support — use original Get path
					if entry, ok := h.Get(r); ok {
						mc.RecordCacheHit(routeID)
						notModified := cache.WriteCachedResponse(w, r, entry, conditional)
						if notModified {
							h.RecordNotModified()
							mc.RecordCacheNotModified(routeID)
						}
						return
					}
				}
				mc.RecordCacheMiss(routeID)
			}

			// Invalidate cache on mutating requests
			if cache.IsMutatingMethod(r.Method) {
				h.InvalidateByPath(r.URL.Path)
			}

			// Wrap writer for cache capture on cacheable requests
			if shouldCache {
				if sie > 0 {
					// stale-if-error: buffer the response so we can fall back to stale on 5xx
					key := h.KeyForRequest(r)
					capWriter := cache.NewCapturingResponseWriter()
					next.ServeHTTP(capWriter, r)

					if capWriter.StatusCode() >= 500 {
						// Check for stale entry to serve instead
						entry, _, stale := h.GetWithStaleness(key)
						if stale && entry != nil {
							mc.RecordCacheHit(routeID)
							writeStaleResponse(w, r, entry, conditional)
							return
						}
					}

					// Write captured response to client
					for k, vv := range capWriter.Header() {
						for _, v := range vv {
							w.Header().Add(k, v)
						}
					}
					w.Header().Set("X-Cache", "MISS")
					w.WriteHeader(capWriter.StatusCode())
					w.Write(capWriter.Body.Bytes())

					// Store if response is cacheable
					sieVarCtx := variables.GetFromRequest(r)
					skipStore := sieVarCtx.SkipFlags&variables.SkipCacheStore != 0
					if !skipStore && h.ShouldStore(capWriter.StatusCode(), capWriter.Header(), int64(capWriter.Body.Len())) {
						entry := &cache.Entry{
							StatusCode: capWriter.StatusCode(),
							Headers:    capWriter.Header().Clone(),
							Body:       capWriter.Body.Bytes(),
						}
						if conditional {
							cache.PopulateConditionalFields(entry)
						}
						if sieVarCtx.Overrides != nil && sieVarCtx.Overrides.CacheTTLOverride > 0 {
							entry.TTL = sieVarCtx.Overrides.CacheTTLOverride
						}
						h.StoreWithMeta(key, r.URL.Path, entry)
					}
					return
				}

				varCtx := variables.GetFromRequest(r)
				if varCtx.SkipFlags&variables.SkipCacheStore != 0 {
					next.ServeHTTP(w, r)
					return
				}

				cachingWriter := cache.NewCachingResponseWriter(w)
				cachingWriter.Header().Set("X-Cache", "MISS")
				next.ServeHTTP(cachingWriter, r)

				// Store if response is cacheable (check skip flag again — may be set by response rules)
				if varCtx.SkipFlags&variables.SkipCacheStore == 0 &&
					h.ShouldStore(cachingWriter.StatusCode(), cachingWriter.Header(), int64(cachingWriter.Body.Len())) {
					entry := &cache.Entry{
						StatusCode: cachingWriter.StatusCode(),
						Headers:    cachingWriter.Header().Clone(),
						Body:       cachingWriter.Body.Bytes(),
					}
					if conditional {
						cache.PopulateConditionalFields(entry)
					}
					if varCtx.Overrides != nil && varCtx.Overrides.CacheTTLOverride > 0 {
						entry.TTL = varCtx.Overrides.CacheTTLOverride
					}
					h.StoreWithMeta(h.KeyForRequest(r), r.URL.Path, entry)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeStaleResponse writes a stale cached entry to the response writer with X-Cache: STALE.
func writeStaleResponse(w http.ResponseWriter, r *http.Request, entry *cache.Entry, conditional bool) {
	for key, values := range entry.Headers {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.Header().Set("X-Cache", "STALE")

	if conditional {
		if entry.ETag != "" {
			w.Header().Set("ETag", entry.ETag)
		}
		if !entry.LastModified.IsZero() {
			w.Header().Set("Last-Modified", entry.LastModified.UTC().Format(http.TimeFormat))
		}
		if cache.CheckConditional(r, entry) {
			w.Header().Del("Content-Length")
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	w.WriteHeader(entry.StatusCode)
	w.Write(entry.Body)
}

// revalidateInBackground runs the inner handler to refresh a stale cache entry.
func revalidateInBackground(h *cache.Handler, next http.Handler, origReq *http.Request, key string, conditional bool) {
	// Clone the request for background use (the original request's context may be cancelled)
	bgReq := origReq.Clone(context.Background())

	capWriter := cache.NewCapturingResponseWriter()
	next.ServeHTTP(capWriter, bgReq)

	// Only store successful responses
	if h.ShouldStore(capWriter.StatusCode(), capWriter.Header(), int64(capWriter.Body.Len())) {
		entry := &cache.Entry{
			StatusCode: capWriter.StatusCode(),
			Headers:    capWriter.Header().Clone(),
			Body:       capWriter.Body.Bytes(),
		}
		if conditional {
			cache.PopulateConditionalFields(entry)
		}
		h.StoreWithMeta(key, origReq.URL.Path, entry)
	}
}

var errServerError = fmt.Errorf("server error")

// 10. circuitBreakerMW checks the circuit breaker and records outcomes.
// If the breaker supports tenant isolation, requests are routed to per-tenant breakers.
func circuitBreakerMW(cb circuitbreaker.BreakerInterface, isGRPC bool) middleware.Middleware {
	tenantCB, isTenantAware := cb.(circuitbreaker.TenantAwareBreakerInterface)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if variables.GetFromRequest(r).SkipFlags&variables.SkipCircuitBreaker != 0 {
				next.ServeHTTP(w, r)
				return
			}
			var done func(error)
			var err error
			if isTenantAware {
				tenantID := ""
				if ti := tenant.FromContext(r.Context()); ti != nil {
					tenantID = ti.ID
				}
				done, err = tenantCB.AllowForTenant(tenantID)
			} else {
				done, err = cb.Allow()
			}
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
			if variables.GetFromRequest(r).SkipFlags&variables.SkipAdaptiveConcurrency != 0 {
				next.ServeHTTP(w, r)
				return
			}
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
		case "set_status":
			rules.ExecuteSetStatus(rw, result.Action.StatusCode)
		case "set_body":
			rules.ExecuteSetBody(rw, result.Action.Body)
		case "lua":
			varCtx := variables.GetFromRequest(r)
			if err := rules.ExecuteLuaResponse(engine.LuaPool(), result.Action.LuaProto, rw, r, varCtx); err != nil {
				logging.Error("lua rule action error",
					zap.String("rule_id", result.RuleID),
					zap.Error(err),
				)
			}
		case "skip_cache_store":
			varCtx := variables.GetFromRequest(r)
			rules.ExecuteSkip(varCtx, variables.SkipCacheStore)
		case "cache_ttl_override":
			varCtx := variables.GetFromRequest(r)
			rules.ExecuteCacheTTLOverride(varCtx, result.Action.CacheTTL)
		}
	}
}

// trafficGroupMW injects A/B variant response header and sticky cookie after proxy completes.
func sessionAffinityMW(sa *loadbalancer.SessionAffinityBalancer) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			saw := &sessionAffinityWriter{ResponseWriter: w, sa: sa, r: r}
			next.ServeHTTP(saw, r)
		})
	}
}

// sessionAffinityWriter intercepts WriteHeader to inject the backend affinity cookie.
type sessionAffinityWriter struct {
	http.ResponseWriter
	sa            *loadbalancer.SessionAffinityBalancer
	r             *http.Request
	headerWritten bool
}

func (w *sessionAffinityWriter) WriteHeader(code int) {
	if !w.headerWritten {
		w.headerWritten = true
		varCtx := variables.GetFromRequest(w.r)
		if varCtx.UpstreamAddr != "" {
			cookie := w.sa.MakeCookie(varCtx.UpstreamAddr)
			http.SetCookie(w.ResponseWriter, cookie)
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *sessionAffinityWriter) Write(b []byte) (int, error) {
	if !w.headerWritten {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}

func (w *sessionAffinityWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

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
			var tenantPriority int
			if ti := tenant.FromContext(r.Context()); ti != nil {
				tenantPriority = ti.Config.Priority
			}
			level := trafficshape.DetermineLevel(r, varCtx.Identity, cfg, tenantPriority)
			if varCtx.Overrides != nil && varCtx.Overrides.PriorityOverride > 0 {
				level = varCtx.Overrides.PriorityOverride
			}

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

// abTestObserverMW records per-traffic-group outcomes for A/B test metric collection.
func abTestObserverMW(ab *abtest.ABTest) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := getStatusRecorder(w)
			next.ServeHTTP(rec, r)

			varCtx := variables.GetFromRequest(r)
			if varCtx.TrafficGroup != "" {
				ab.RecordRequest(varCtx.TrafficGroup, rec.statusCode, time.Since(start))
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
