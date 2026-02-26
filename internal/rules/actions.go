package rules

import (
	"net/http"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/logging"
	"github.com/wudi/runway/variables"
	"go.uber.org/zap"
)

// ExecuteTerminatingAction writes the appropriate response for a terminating action.
func ExecuteTerminatingAction(w http.ResponseWriter, r *http.Request, action Action) {
	switch action.Type {
	case "block":
		status := action.StatusCode
		if status == 0 {
			status = http.StatusForbidden
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		if action.Body != "" {
			w.Write([]byte(action.Body))
		} else {
			w.Write([]byte(http.StatusText(status)))
		}

	case "custom_response":
		status := action.StatusCode
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		if action.Body != "" {
			w.Write([]byte(action.Body))
		}

	case "redirect":
		status := action.StatusCode
		if status == 0 {
			status = http.StatusFound
		}
		http.Redirect(w, r, action.RedirectURL, status)
	}
}

// ExecuteRequestHeaders modifies request headers in-place (non-terminating).
func ExecuteRequestHeaders(r *http.Request, headers config.HeaderTransform) {
	for k, v := range headers.Add {
		r.Header.Add(k, v)
	}
	for k, v := range headers.Set {
		r.Header.Set(k, v)
	}
	for _, k := range headers.Remove {
		r.Header.Del(k)
	}
}

// ExecuteResponseHeaders modifies response headers (non-terminating).
func ExecuteResponseHeaders(w http.ResponseWriter, headers config.HeaderTransform) {
	for k, v := range headers.Add {
		w.Header().Add(k, v)
	}
	for k, v := range headers.Set {
		w.Header().Set(k, v)
	}
	for _, k := range headers.Remove {
		w.Header().Del(k)
	}
}

// ExecuteRewrite rewrites the request URI path, query string, and/or headers (non-terminating).
func ExecuteRewrite(r *http.Request, cfg *config.RewriteActionConfig) {
	if cfg.Path != "" {
		r.URL.Path = cfg.Path
	}
	if cfg.Query != "" {
		r.URL.RawQuery = cfg.Query
	}
	ExecuteRequestHeaders(r, cfg.Headers)
}

// ExecuteGroup assigns the request to a named traffic split group (non-terminating).
func ExecuteGroup(varCtx *variables.Context, groupName string) {
	varCtx.TrafficGroup = groupName
}

// ExecuteLog logs a matched request-phase rule with structured context (non-terminating).
func ExecuteLog(ruleID string, r *http.Request, varCtx *variables.Context, message string) {
	fields := []zap.Field{
		zap.String("rule_id", ruleID),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
	}
	if varCtx != nil && varCtx.RouteID != "" {
		fields = append(fields, zap.String("route_id", varCtx.RouteID))
	}
	if message != "" {
		fields = append(fields, zap.String("message", message))
	}
	logging.Info("rule_log", fields...)
}

// ExecuteResponseLog logs a matched response-phase rule with structured context (non-terminating).
func ExecuteResponseLog(ruleID string, r *http.Request, statusCode int, message string) {
	fields := []zap.Field{
		zap.String("rule_id", ruleID),
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr),
		zap.Int("status", statusCode),
	}
	if message != "" {
		fields = append(fields, zap.String("message", message))
	}
	logging.Info("rule_log", fields...)
}

// ExecuteDelay pauses the request for the given duration (non-terminating).
func ExecuteDelay(d time.Duration) {
	time.Sleep(d)
}

// ExecuteSetVar writes key-value pairs to the variable context (non-terminating).
func ExecuteSetVar(varCtx *variables.Context, vars map[string]string) {
	if varCtx == nil {
		return
	}
	if varCtx.Custom == nil {
		varCtx.Custom = make(map[string]string, len(vars))
	}
	for k, v := range vars {
		varCtx.Custom[k] = v
	}
}

// ExecuteSetStatus updates the buffered response status code (non-terminating).
func ExecuteSetStatus(rw *RulesResponseWriter, code int) {
	rw.SetStatusCode(code)
}

// ExecuteSetBody replaces the buffered response body (non-terminating).
func ExecuteSetBody(rw *RulesResponseWriter, body string) {
	rw.SetBody(body)
}

// ExecuteSkip sets a skip flag on the variable context (non-terminating).
func ExecuteSkip(varCtx *variables.Context, flag variables.SkipFlags) {
	varCtx.SkipFlags |= flag
}

// ensureOverrides lazy-initializes the Overrides pointer on the variable context.
func ensureOverrides(varCtx *variables.Context) *variables.ValueOverrides {
	if varCtx.Overrides == nil {
		varCtx.Overrides = &variables.ValueOverrides{}
	}
	return varCtx.Overrides
}

// ExecuteRateLimitTier overrides the rate limit tier (non-terminating).
func ExecuteRateLimitTier(varCtx *variables.Context, tier string) {
	ensureOverrides(varCtx).RateLimitTier = tier
}

// ExecuteTimeoutOverride overrides the request timeout (non-terminating).
func ExecuteTimeoutOverride(varCtx *variables.Context, d time.Duration) {
	ensureOverrides(varCtx).TimeoutOverride = d
}

// ExecutePriorityOverride overrides the priority admission level (non-terminating).
func ExecutePriorityOverride(varCtx *variables.Context, level int) {
	ensureOverrides(varCtx).PriorityOverride = level
}

// ExecuteBandwidthOverride overrides the bandwidth limit (non-terminating).
func ExecuteBandwidthOverride(varCtx *variables.Context, bw int64) {
	ensureOverrides(varCtx).BandwidthOverride = bw
}

// ExecuteBodyLimitOverride overrides the body size limit (non-terminating).
func ExecuteBodyLimitOverride(varCtx *variables.Context, limit int64) {
	ensureOverrides(varCtx).BodyLimitOverride = limit
}

// ExecuteSwitchBackend overrides the backend selection (non-terminating).
func ExecuteSwitchBackend(varCtx *variables.Context, backend string) {
	ensureOverrides(varCtx).SwitchBackend = backend
}

// ExecuteCacheTTLOverride overrides the cache TTL (non-terminating).
func ExecuteCacheTTLOverride(varCtx *variables.Context, ttl time.Duration) {
	ensureOverrides(varCtx).CacheTTLOverride = ttl
}
