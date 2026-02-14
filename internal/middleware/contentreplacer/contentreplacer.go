package contentreplacer

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
)

// compiledRule holds a pre-compiled replacement rule.
type compiledRule struct {
	regex       *regexp.Regexp
	replacement string
	scope       string // "body" or "header:<name>"
	headerName  string // only set when scope starts with "header:"
}

// ContentReplacer applies regex replacements to response body and/or headers.
type ContentReplacer struct {
	rules    []compiledRule
	total    atomic.Int64
	replaced atomic.Int64
}

// New creates a ContentReplacer from config. All patterns must be valid regexes.
func New(cfg config.ContentReplacerConfig) (*ContentReplacer, error) {
	rules := make([]compiledRule, 0, len(cfg.Replacements))
	for _, rule := range cfg.Replacements {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, err
		}
		scope := rule.Scope
		if scope == "" {
			scope = "body"
		}
		cr := compiledRule{
			regex:       re,
			replacement: rule.Replacement,
			scope:       scope,
		}
		if strings.HasPrefix(scope, "header:") {
			cr.headerName = strings.TrimPrefix(scope, "header:")
		}
		rules = append(rules, cr)
	}
	return &ContentReplacer{rules: rules}, nil
}

// isTextContent returns true if the Content-Type is text-like and should be processed.
func isTextContent(ct string) bool {
	ct = strings.ToLower(ct)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	if strings.Contains(ct, "application/json") {
		return true
	}
	if strings.Contains(ct, "application/xml") {
		return true
	}
	if strings.Contains(ct, "application/xhtml") {
		return true
	}
	return false
}

// Middleware returns a middleware that applies content replacements.
func (cr *ContentReplacer) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bw := &bufferingWriter{
				ResponseWriter: w,
				cr:             cr,
			}
			next.ServeHTTP(bw, r)
			bw.flush()
		})
	}
}

// Stats returns metrics for this replacer.
func (cr *ContentReplacer) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total":    cr.total.Load(),
		"replaced": cr.replaced.Load(),
		"rules":    len(cr.rules),
	}
}

// bufferingWriter buffers the response body so replacements can be applied.
type bufferingWriter struct {
	http.ResponseWriter
	cr          *ContentReplacer
	buf         bytes.Buffer
	statusCode  int
	wroteHeader bool
}

func (w *bufferingWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.statusCode = code
}

func (w *bufferingWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.buf.Write(b)
}

func (w *bufferingWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *bufferingWriter) flush() {
	w.cr.total.Add(1)

	// Apply header replacements
	anyReplaced := false
	for _, rule := range w.cr.rules {
		if rule.headerName != "" {
			v := w.ResponseWriter.Header().Get(rule.headerName)
			if v != "" {
				newV := rule.regex.ReplaceAllString(v, rule.replacement)
				if newV != v {
					w.ResponseWriter.Header().Set(rule.headerName, newV)
					anyReplaced = true
				}
			}
		}
	}

	body := w.buf.Bytes()

	// Apply body replacements only for text content types
	ct := w.ResponseWriter.Header().Get("Content-Type")
	if isTextContent(ct) && len(body) > 0 {
		for _, rule := range w.cr.rules {
			if rule.scope == "body" {
				newBody := rule.regex.ReplaceAll(body, []byte(rule.replacement))
				if !bytes.Equal(newBody, body) {
					body = newBody
					anyReplaced = true
				}
			}
		}
	}

	if anyReplaced {
		w.cr.replaced.Add(1)
	}

	// Remove Content-Length since body may have changed size
	w.ResponseWriter.Header().Del("Content-Length")

	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.statusCode)
	w.ResponseWriter.Write(body)
}

// ContentReplacerByRoute manages per-route content replacers.
type ContentReplacerByRoute struct {
	replacers map[string]*ContentReplacer
	mu        sync.RWMutex
}

// NewContentReplacerByRoute creates a new per-route content replacer manager.
func NewContentReplacerByRoute() *ContentReplacerByRoute {
	return &ContentReplacerByRoute{
		replacers: make(map[string]*ContentReplacer),
	}
}

// AddRoute adds a content replacer for a route.
func (m *ContentReplacerByRoute) AddRoute(routeID string, cfg config.ContentReplacerConfig) error {
	cr, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.replacers[routeID] = cr
	m.mu.Unlock()
	return nil
}

// GetReplacer returns the content replacer for a route.
func (m *ContentReplacerByRoute) GetReplacer(routeID string) *ContentReplacer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.replacers[routeID]
}

// RouteIDs returns all route IDs with content replacers.
func (m *ContentReplacerByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.replacers))
	for id := range m.replacers {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route content replacer metrics.
func (m *ContentReplacerByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := make(map[string]interface{}, len(m.replacers))
	for id, cr := range m.replacers {
		stats[id] = cr.Stats()
	}
	return stats
}
