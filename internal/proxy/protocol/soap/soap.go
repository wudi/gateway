package soap

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"text/template"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/middleware/backendenc"
	"github.com/wudi/runway/internal/tmplutil"
	"github.com/wudi/runway/variables"
)

// Handler translates REST requests to SOAP calls.
type Handler struct {
	url         string
	tmpl        *template.Template
	contentType string
	transport   http.RoundTripper

	totalRequests atomic.Int64
	totalErrors   atomic.Int64
}

type templateContext struct {
	Method     string
	Path       string
	Host       string
	PathParams map[string]string
	Query      map[string][]string
	Headers    http.Header
}

// New creates a SOAP handler from config.
func New(cfg config.SOAPProtocolConfig, transport http.RoundTripper) (*Handler, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("soap: url is required")
	}
	if cfg.Template == "" {
		return nil, fmt.Errorf("soap: template is required")
	}

	tmpl, err := template.New("soap_envelope").Funcs(tmplutil.FuncMap()).Parse(cfg.Template)
	if err != nil {
		return nil, fmt.Errorf("soap: invalid template: %w", err)
	}

	ct := cfg.ContentType
	if ct == "" {
		ct = "text/xml; charset=utf-8"
	}

	return &Handler{
		url:         cfg.URL,
		tmpl:        tmpl,
		contentType: ct,
		transport:   transport,
	}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.totalRequests.Add(1)

	varCtx := variables.GetFromRequest(r)
	ctx := templateContext{
		Method:  r.Method,
		Path:    r.URL.Path,
		Host:    r.Host,
		Query:   r.URL.Query(),
		Headers: r.Header,
	}
	if varCtx != nil {
		ctx.PathParams = varCtx.PathParams
	}

	// Render SOAP envelope
	var envBuf bytes.Buffer
	if err := h.tmpl.Execute(&envBuf, ctx); err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "soap: template error", http.StatusBadGateway)
		return
	}

	// Send SOAP request
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.url, &envBuf)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "soap: request creation error", http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", h.contentType)

	resp, err := h.transport.RoundTrip(req)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "soap: request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "soap: read response failed", http.StatusBadGateway)
		return
	}

	// Convert XML response to JSON
	jsonBody, err := backendenc.DecodeBytes(respBody, "xml")
	if err != nil {
		// Fall back to raw XML passthrough
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(jsonBody)
}

// Stats returns handler stats.
func (h *Handler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total_requests": h.totalRequests.Load(),
		"total_errors":   h.totalErrors.Load(),
		"url":            h.url,
	}
}

// SOAPByRoute manages per-route SOAP handlers.
type SOAPByRoute struct {
	byroute.Manager[*Handler]
}

// NewSOAPByRoute creates a new manager.
func NewSOAPByRoute() *SOAPByRoute {
	return &SOAPByRoute{}
}

// AddRoute adds a SOAP handler for a route.
func (m *SOAPByRoute) AddRoute(routeID string, cfg config.SOAPProtocolConfig, transport http.RoundTripper) error {
	h, err := New(cfg, transport)
	if err != nil {
		return err
	}
	m.Add(routeID, h)
	return nil
}

// GetHandler returns the handler for a route.
func (m *SOAPByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route stats.
func (m *SOAPByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *Handler) interface{} { return h.Stats() })
}
