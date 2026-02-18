package waf

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/types"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/logging"
	"github.com/wudi/gateway/internal/middleware"
	"go.uber.org/zap"
)

// WAF wraps coraza WAF engine for a single route.
type WAF struct {
	engine coraza.WAF
	mode   string // "block" or "detect"

	// Metrics
	requestsTotal atomic.Int64
	blockedTotal  atomic.Int64
	detectedTotal atomic.Int64
}

// New creates a new WAF from config.
func New(cfg config.WAFConfig) (*WAF, error) {
	wafCfg := coraza.NewWAFConfig()

	// Apply inline rules
	for _, rule := range cfg.InlineRules {
		wafCfg = wafCfg.WithDirectives(rule)
	}

	// Apply rule files
	for _, path := range cfg.RuleFiles {
		wafCfg = wafCfg.WithDirectives(fmt.Sprintf("Include %s", path))
	}

	// Apply built-in rule sets
	if cfg.SQLInjection {
		wafCfg = wafCfg.WithDirectives(`
			SecRule ARGS|ARGS_NAMES|REQUEST_BODY "@detectSQLi" "id:1001,phase:2,deny,status:403,msg:'SQL Injection detected',tag:'attack-sqli'"
		`)
	}
	if cfg.XSS {
		wafCfg = wafCfg.WithDirectives(`
			SecRule ARGS|ARGS_NAMES|REQUEST_BODY "@detectXSS" "id:1002,phase:2,deny,status:403,msg:'XSS detected',tag:'attack-xss'"
		`)
	}

	engine, err := coraza.NewWAF(wafCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize WAF: %w", err)
	}

	mode := cfg.Mode
	if mode == "" {
		mode = "block"
	}

	return &WAF{
		engine: engine,
		mode:   mode,
	}, nil
}

// Middleware returns an HTTP middleware that runs WAF inspection.
func (w *WAF) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			w.requestsTotal.Add(1)

			tx := w.engine.NewTransaction()
			defer func() {
				tx.ProcessLogging()
				if err := tx.Close(); err != nil {
					logging.Error("WAF transaction close error", zap.Error(err))
				}
			}()

			// Process connection and URI
			tx.ProcessConnection(clientIP(r), 0, "", 0)
			tx.ProcessURI(r.URL.String(), r.Method, r.Proto)

			// Process request headers
			for k, vv := range r.Header {
				for _, v := range vv {
					tx.AddRequestHeader(k, v)
				}
			}

			if it := tx.ProcessRequestHeaders(); it != nil {
				w.handleInterruption(it, rw)
				return
			}

			// Process request body if present
			if r.Body != nil && r.ContentLength > 0 {
				it, _, err := tx.ReadRequestBodyFrom(r.Body)
				if err != nil {
					logging.Error("WAF request body read error", zap.Error(err))
				}
				if it != nil {
					w.handleInterruption(it, rw)
					return
				}
				r.Body.Close()

				// Replace body with what coraza buffered
				reader, err := tx.RequestBodyReader()
				if err == nil {
					r.Body = readCloser{reader}
				}
			}

			it, err := tx.ProcessRequestBody()
			if err != nil {
				logging.Error("WAF process request body error", zap.Error(err))
			}
			if it != nil {
				w.handleInterruption(it, rw)
				return
			}

			next.ServeHTTP(rw, r)
		})
	}
}

// readCloser wraps an io.Reader with a no-op Close.
type readCloser struct {
	r interface{ Read([]byte) (int, error) }
}

func (rc readCloser) Read(p []byte) (int, error) { return rc.r.Read(p) }
func (rc readCloser) Close() error               { return nil }

// handleInterruption handles a WAF interruption (block or detect mode).
func (w *WAF) handleInterruption(it *types.Interruption, rw http.ResponseWriter) {
	if w.mode == "detect" {
		w.detectedTotal.Add(1)
		logging.Warn("WAF detected threat (detect mode, not blocking)",
			zap.Int("status", it.Status),
			zap.String("action", it.Action),
		)
		return
	}

	w.blockedTotal.Add(1)
	logging.Warn("WAF blocked request",
		zap.Int("status", it.Status),
		zap.String("action", it.Action),
	)
	status := it.Status
	if status == 0 {
		status = http.StatusForbidden
	}
	rw.WriteHeader(status)
	rw.Write([]byte(`{"error":"request blocked by WAF"}`))
}

// Stats returns metrics snapshot.
func (w *WAF) Stats() map[string]interface{} {
	return map[string]interface{}{
		"mode":           w.mode,
		"requests_total": w.requestsTotal.Load(),
		"blocked_total":  w.blockedTotal.Load(),
		"detected_total": w.detectedTotal.Load(),
	}
}

// clientIP extracts client IP from the request.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	host := r.RemoteAddr
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	return host
}

// WAFByRoute manages WAF instances per route.
type WAFByRoute struct {
	wafs map[string]*WAF
	mu   sync.RWMutex
}

// NewWAFByRoute creates a new per-route WAF manager.
func NewWAFByRoute() *WAFByRoute {
	return &WAFByRoute{}
}

// AddRoute adds a WAF for a route. Returns error if WAF init fails.
func (m *WAFByRoute) AddRoute(routeID string, cfg config.WAFConfig) error {
	w, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	if m.wafs == nil {
		m.wafs = make(map[string]*WAF)
	}
	m.wafs[routeID] = w
	m.mu.Unlock()
	return nil
}

// GetWAF returns the WAF for a route, or nil if not configured.
func (m *WAFByRoute) GetWAF(routeID string) *WAF {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.wafs[routeID]
}

// RouteIDs returns all route IDs with WAFs.
func (m *WAFByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.wafs))
	for id := range m.wafs {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns WAF stats for all routes.
func (m *WAFByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]interface{}, len(m.wafs))
	for id, w := range m.wafs {
		result[id] = w.Stats()
	}
	return result
}
