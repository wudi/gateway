package piiredact

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
)

// Built-in PII pattern regexes.
var builtInPatterns = map[string]*regexp.Regexp{
	"email":       regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`),
	"credit_card": regexp.MustCompile(`\b(?:\d[ \-]?){13,16}\b`),
	"ssn":         regexp.MustCompile(`\b\d{3}[- ]\d{2}[- ]\d{4}\b`),
	"phone":       regexp.MustCompile(`\b(?:\+?1[-.\s]?)?\(?\d{3}\)?[-.\s]?\d{3}[-.\s]?\d{4}\b`),
}

type compiledPattern struct {
	name        string
	regex       *regexp.Regexp
	replacement string // if empty, uses maskChar fill
}

// PIIRedactor applies PII pattern redaction to request/response bodies and headers.
type PIIRedactor struct {
	patterns []compiledPattern
	scope    string // "response", "request", "both"
	maskChar string
	headers  []string
	total    atomic.Int64
	redacted atomic.Int64
}

// New creates a PIIRedactor from config.
func New(cfg config.PIIRedactionConfig) (*PIIRedactor, error) {
	maskChar := cfg.MaskChar
	if maskChar == "" {
		maskChar = "*"
	}

	scope := cfg.Scope
	if scope == "" {
		scope = "response"
	}

	var patterns []compiledPattern
	for _, name := range cfg.BuiltIns {
		re, ok := builtInPatterns[name]
		if !ok {
			continue
		}
		patterns = append(patterns, compiledPattern{
			name:  name,
			regex: re,
		})
	}
	for _, custom := range cfg.Custom {
		re, err := regexp.Compile(custom.Pattern)
		if err != nil {
			return nil, err
		}
		patterns = append(patterns, compiledPattern{
			name:        custom.Name,
			regex:       re,
			replacement: custom.Replacement,
		})
	}

	return &PIIRedactor{
		patterns: patterns,
		scope:    scope,
		maskChar: maskChar,
		headers:  cfg.Headers,
	}, nil
}

func (pr *PIIRedactor) redact(data []byte) []byte {
	for _, p := range pr.patterns {
		if p.replacement != "" {
			data = p.regex.ReplaceAll(data, []byte(p.replacement))
		} else {
			data = p.regex.ReplaceAllFunc(data, func(match []byte) []byte {
				return []byte(strings.Repeat(pr.maskChar, len(match)))
			})
		}
	}
	return data
}

func (pr *PIIRedactor) redactString(s string) string {
	for _, p := range pr.patterns {
		if p.replacement != "" {
			s = p.regex.ReplaceAllString(s, p.replacement)
		} else {
			s = p.regex.ReplaceAllStringFunc(s, func(match string) string {
				return strings.Repeat(pr.maskChar, len(match))
			})
		}
	}
	return s
}

func isTextContent(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "application/json") ||
		strings.Contains(ct, "application/xml") ||
		strings.Contains(ct, "application/xhtml")
}

// Middleware returns a middleware that applies PII redaction.
func (pr *PIIRedactor) Middleware() middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			pr.total.Add(1)

			// Redact request body if scope includes request
			if (pr.scope == "request" || pr.scope == "both") && r.Body != nil {
				ct := r.Header.Get("Content-Type")
				if isTextContent(ct) {
					body, err := io.ReadAll(r.Body)
					if err == nil && len(body) > 0 {
						redacted := pr.redact(body)
						if !bytes.Equal(redacted, body) {
							pr.redacted.Add(1)
						}
						r.Body = io.NopCloser(bytes.NewReader(redacted))
						r.ContentLength = int64(len(redacted))
					}
				}
			}

			// Redact request headers
			if pr.scope == "request" || pr.scope == "both" {
				for _, h := range pr.headers {
					if v := r.Header.Get(h); v != "" {
						r.Header.Set(h, pr.redactString(v))
					}
				}
			}

			// For response redaction, wrap the response writer
			if pr.scope == "response" || pr.scope == "both" {
				bw := &bufferingWriter{
					ResponseWriter: w,
					pr:             pr,
				}
				next.ServeHTTP(bw, r)
				bw.flush()
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// Stats returns metrics for this redactor.
func (pr *PIIRedactor) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total":    pr.total.Load(),
		"redacted": pr.redacted.Load(),
		"patterns": len(pr.patterns),
		"scope":    pr.scope,
	}
}

// bufferingWriter buffers the response body so PII redaction can be applied.
type bufferingWriter struct {
	http.ResponseWriter
	pr          *PIIRedactor
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

func (w *bufferingWriter) flush() {
	// Redact response headers
	for _, h := range w.pr.headers {
		if v := w.ResponseWriter.Header().Get(h); v != "" {
			w.ResponseWriter.Header().Set(h, w.pr.redactString(v))
		}
	}

	body := w.buf.Bytes()

	// Redact body for text content types
	ct := w.ResponseWriter.Header().Get("Content-Type")
	if isTextContent(ct) && len(body) > 0 {
		redacted := w.pr.redact(body)
		if !bytes.Equal(redacted, body) {
			w.pr.redacted.Add(1)
			body = redacted
		}
	}

	w.ResponseWriter.Header().Del("Content-Length")
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(w.statusCode)
	w.ResponseWriter.Write(body)
}

// PIIRedactByRoute manages per-route PII redactors.
type PIIRedactByRoute struct {
	byroute.Manager[*PIIRedactor]
}

// NewPIIRedactByRoute creates a new manager.
func NewPIIRedactByRoute() *PIIRedactByRoute {
	return &PIIRedactByRoute{}
}

// AddRoute adds a PII redactor for a route.
func (m *PIIRedactByRoute) AddRoute(routeID string, cfg config.PIIRedactionConfig) error {
	pr, err := New(cfg)
	if err != nil {
		return err
	}
	m.Add(routeID, pr)
	return nil
}

// GetRedactor returns the PII redactor for a route.
func (m *PIIRedactByRoute) GetRedactor(routeID string) *PIIRedactor {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route PII redaction metrics.
func (m *PIIRedactByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(pr *PIIRedactor) interface{} { return pr.Stats() })
}
