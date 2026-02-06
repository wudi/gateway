package rules

import (
	"net/http"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/logging"
	"github.com/example/gateway/internal/variables"
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
