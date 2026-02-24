package errorpages

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/variables"
)

// TemplateData is the data exposed to error page templates.
type TemplateData struct {
	StatusCode    int
	StatusText    string
	ErrorMessage  string
	RequestID     string
	RequestMethod string
	RequestPath   string
	Host          string
	Timestamp     string
	RouteID       string
}

// compiledPage holds pre-compiled templates for a single error page entry.
type compiledPage struct {
	html *template.Template
	json *template.Template
	xml  *template.Template
}

// CompiledErrorPages holds pre-compiled error page templates for a route.
type CompiledErrorPages struct {
	exactPages  map[int]*compiledPage // exact status code → page (e.g. 404)
	classPages  map[int]*compiledPage // class base → page (e.g. 400 for "4xx")
	defaultPage *compiledPage
	metrics     *ErrorPagesMetrics
}

// New merges global + per-route error page configs and compiles templates.
// Per-route keys override global keys. Returns nil if no pages are configured.
func New(global, perRoute config.ErrorPagesConfig) (*CompiledErrorPages, error) {
	if !global.IsActive() && !perRoute.IsActive() {
		return nil, nil
	}

	merged := make(map[string]config.ErrorPageEntry)
	if global.Enabled {
		for k, v := range global.Pages {
			merged[k] = v
		}
	}
	if perRoute.Enabled {
		for k, v := range perRoute.Pages {
			merged[k] = v
		}
	}

	if len(merged) == 0 {
		return nil, nil
	}

	ep := &CompiledErrorPages{
		exactPages: make(map[int]*compiledPage),
		classPages: make(map[int]*compiledPage),
		metrics:    &ErrorPagesMetrics{},
	}

	for key, entry := range merged {
		cp, err := compilePage(key, entry)
		if err != nil {
			return nil, fmt.Errorf("error_pages key %q: %w", key, err)
		}

		if key == "default" {
			ep.defaultPage = cp
		} else if len(key) == 3 && key[1] == 'x' && key[2] == 'x' {
			// Class pattern like "4xx", "5xx"
			base := int(key[0]-'0') * 100
			ep.classPages[base] = cp
		} else {
			code, err := strconv.Atoi(key)
			if err != nil {
				return nil, fmt.Errorf("error_pages key %q: must be a status code, class pattern (e.g. 4xx), or \"default\"", key)
			}
			ep.exactPages[code] = cp
		}
	}

	return ep, nil
}

// compilePage reads file templates and compiles all formats for an entry.
func compilePage(key string, entry config.ErrorPageEntry) (*compiledPage, error) {
	cp := &compiledPage{}
	var err error

	htmlTmpl := entry.HTML
	if entry.HTMLFile != "" {
		htmlTmpl, err = readFile(entry.HTMLFile)
		if err != nil {
			return nil, fmt.Errorf("html_file: %w", err)
		}
	}
	if htmlTmpl != "" {
		cp.html, err = template.New(key + "-html").Parse(htmlTmpl)
		if err != nil {
			return nil, fmt.Errorf("html template: %w", err)
		}
	}

	jsonTmpl := entry.JSON
	if entry.JSONFile != "" {
		jsonTmpl, err = readFile(entry.JSONFile)
		if err != nil {
			return nil, fmt.Errorf("json_file: %w", err)
		}
	}
	if jsonTmpl != "" {
		cp.json, err = template.New(key + "-json").Parse(jsonTmpl)
		if err != nil {
			return nil, fmt.Errorf("json template: %w", err)
		}
	}

	xmlTmpl := entry.XML
	if entry.XMLFile != "" {
		xmlTmpl, err = readFile(entry.XMLFile)
		if err != nil {
			return nil, fmt.Errorf("xml_file: %w", err)
		}
	}
	if xmlTmpl != "" {
		cp.xml, err = template.New(key + "-xml").Parse(xmlTmpl)
		if err != nil {
			return nil, fmt.Errorf("xml template: %w", err)
		}
	}

	return cp, nil
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ShouldIntercept returns true if the status code is an error (>= 400) and
// a matching page exists.
func (ep *CompiledErrorPages) ShouldIntercept(statusCode int) bool {
	if statusCode < 400 {
		return false
	}
	return ep.findPage(statusCode) != nil
}

// findPage implements the fallback chain: exact → class → default → nil.
func (ep *CompiledErrorPages) findPage(statusCode int) *compiledPage {
	// Exact match
	if p, ok := ep.exactPages[statusCode]; ok {
		return p
	}
	// Class match (e.g. 404 → 400)
	classBase := (statusCode / 100) * 100
	if p, ok := ep.classPages[classBase]; ok {
		return p
	}
	// Default
	return ep.defaultPage
}

// Render renders the error page for the given status code, negotiating content
// type from the Accept header.
func (ep *CompiledErrorPages) Render(statusCode int, r *http.Request, varCtx *variables.Context) (body string, contentType string) {
	ep.metrics.TotalRendered.Add(1)

	page := ep.findPage(statusCode)
	if page == nil {
		return defaultBody("json", newTemplateData(statusCode, r, varCtx)), "application/json"
	}

	format := negotiateFormat(r.Header.Get("Accept"), page)
	data := newTemplateData(statusCode, r, varCtx)

	var tmpl *template.Template
	switch format {
	case "html":
		tmpl = page.html
		contentType = "text/html; charset=utf-8"
	case "xml":
		tmpl = page.xml
		contentType = "application/xml; charset=utf-8"
	default:
		tmpl = page.json
		contentType = "application/json"
	}

	if tmpl == nil {
		return defaultBody(format, data), contentType
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return defaultBody(format, data), contentType
	}
	return sb.String(), contentType
}

// Metrics returns the current metrics snapshot.
func (ep *CompiledErrorPages) Metrics() ErrorPagesSnapshot {
	return ErrorPagesSnapshot{
		TotalRendered: ep.metrics.TotalRendered.Load(),
	}
}

// negotiateFormat checks the Accept header and returns the best format.
func negotiateFormat(accept string, page *compiledPage) string {
	accept = strings.ToLower(accept)

	// Check explicit preferences
	if strings.Contains(accept, "text/html") && page.html != nil {
		return "html"
	}
	if (strings.Contains(accept, "application/xml") || strings.Contains(accept, "text/xml")) && page.xml != nil {
		return "xml"
	}
	if strings.Contains(accept, "application/json") && page.json != nil {
		return "json"
	}

	// Fallback: pick the best available format
	if page.json != nil {
		return "json"
	}
	if page.html != nil {
		return "html"
	}
	if page.xml != nil {
		return "xml"
	}
	return "json"
}

func newTemplateData(statusCode int, r *http.Request, varCtx *variables.Context) TemplateData {
	data := TemplateData{
		StatusCode:    statusCode,
		StatusText:    http.StatusText(statusCode),
		ErrorMessage:  http.StatusText(statusCode),
		Timestamp:     time.Now().Format(time.RFC3339),
		RequestMethod: r.Method,
		RequestPath:   r.URL.Path,
		Host:          r.Host,
	}
	if varCtx != nil {
		data.RequestID = varCtx.RequestID
		data.RouteID = varCtx.RouteID
	}
	return data
}

// defaultBody generates a minimal error body when a format template is missing.
func defaultBody(format string, data TemplateData) string {
	switch format {
	case "html":
		return fmt.Sprintf("<html><body><h1>%d %s</h1></body></html>", data.StatusCode, data.StatusText)
	case "xml":
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><error><code>%d</code><message>%s</message></error>`, data.StatusCode, data.StatusText)
	default:
		return fmt.Sprintf(`{"code":%d,"message":"%s"}`, data.StatusCode, data.StatusText)
	}
}

// Middleware returns a middleware that intercepts error responses and renders custom error pages.
func (ep *CompiledErrorPages) Middleware() func(http.Handler) http.Handler {
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

type errorPageWriter struct {
	http.ResponseWriter
	ep          *CompiledErrorPages
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
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func (w *errorPageWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
