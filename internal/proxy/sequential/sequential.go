package sequential

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/variables"
)

// StepContext is the template context available to each step's templates.
type StepContext struct {
	Request struct {
		Method     string
		URL        string
		Host       string
		Path       string
		PathParams map[string]string
		Query      url.Values
		Headers    http.Header
	}
	// Resp0, Resp1, etc. are populated dynamically — we use a map for flexibility.
	Responses map[string]interface{}
}

// compiledStep holds pre-compiled templates for a single step.
type compiledStep struct {
	urlTmpl     *template.Template
	method      string
	headerTmpls map[string]*template.Template
	bodyTmpl    *template.Template
	timeout     time.Duration
}

// SequentialHandler chains multiple backend calls where each step's response
// feeds into the next step's template context.
type SequentialHandler struct {
	steps     []compiledStep
	transport http.RoundTripper

	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	stepErrors    []atomic.Int64
	stepLatencies []atomic.Int64 // accumulated microseconds
}

var funcMap = template.FuncMap{
	"json": func(v interface{}) string {
		b, _ := json.Marshal(v)
		return string(b)
	},
}

// New creates a SequentialHandler from config.
func New(cfg config.SequentialConfig, transport http.RoundTripper) (*SequentialHandler, error) {
	steps := make([]compiledStep, len(cfg.Steps))
	stepErrors := make([]atomic.Int64, len(cfg.Steps))
	stepLatencies := make([]atomic.Int64, len(cfg.Steps))

	for i, s := range cfg.Steps {
		urlTmpl, err := template.New(fmt.Sprintf("step%d_url", i)).Funcs(funcMap).Parse(s.URL)
		if err != nil {
			return nil, fmt.Errorf("step %d: invalid URL template: %w", i, err)
		}

		method := s.Method
		if method == "" {
			method = http.MethodGet
		}

		timeout := s.Timeout
		if timeout == 0 {
			timeout = 5 * time.Second
		}

		cs := compiledStep{
			urlTmpl: urlTmpl,
			method:  method,
			timeout: timeout,
		}

		if len(s.Headers) > 0 {
			cs.headerTmpls = make(map[string]*template.Template, len(s.Headers))
			for k, v := range s.Headers {
				ht, err := template.New(fmt.Sprintf("step%d_header_%s", i, k)).Funcs(funcMap).Parse(v)
				if err != nil {
					return nil, fmt.Errorf("step %d: invalid header template for %s: %w", i, k, err)
				}
				cs.headerTmpls[k] = ht
			}
		}

		if s.BodyTemplate != "" {
			bt, err := template.New(fmt.Sprintf("step%d_body", i)).Funcs(funcMap).Parse(s.BodyTemplate)
			if err != nil {
				return nil, fmt.Errorf("step %d: invalid body template: %w", i, err)
			}
			cs.bodyTmpl = bt
		}

		steps[i] = cs
	}

	return &SequentialHandler{
		steps:         steps,
		transport:     transport,
		stepErrors:    stepErrors,
		stepLatencies: stepLatencies,
	}, nil
}

func (sh *SequentialHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sh.totalRequests.Add(1)
	varCtx := variables.GetFromRequest(r)

	// Build initial context
	sctx := &StepContext{
		Responses: make(map[string]interface{}),
	}
	sctx.Request.Method = r.Method
	sctx.Request.URL = r.URL.String()
	sctx.Request.Host = r.Host
	sctx.Request.Path = r.URL.Path
	sctx.Request.PathParams = varCtx.PathParams
	sctx.Request.Query = r.URL.Query()
	sctx.Request.Headers = r.Header

	var lastResp *http.Response

	for i, step := range sh.steps {
		start := time.Now()

		// Render URL
		var urlBuf bytes.Buffer
		if err := step.urlTmpl.Execute(&urlBuf, sctx); err != nil {
			sh.stepErrors[i].Add(1)
			sh.totalErrors.Add(1)
			http.Error(w, fmt.Sprintf("step %d: URL template error", i), http.StatusBadGateway)
			return
		}

		targetURL := urlBuf.String()

		// Render body
		var body io.Reader
		if step.bodyTmpl != nil {
			var bodyBuf bytes.Buffer
			if err := step.bodyTmpl.Execute(&bodyBuf, sctx); err != nil {
				sh.stepErrors[i].Add(1)
				sh.totalErrors.Add(1)
				http.Error(w, fmt.Sprintf("step %d: body template error", i), http.StatusBadGateway)
				return
			}
			body = &bodyBuf
		}

		// Create request with per-step timeout
		ctx, cancel := context.WithTimeout(r.Context(), step.timeout)
		stepReq, err := http.NewRequestWithContext(ctx, step.method, targetURL, body)
		if err != nil {
			cancel()
			sh.stepErrors[i].Add(1)
			sh.totalErrors.Add(1)
			http.Error(w, fmt.Sprintf("step %d: request creation error", i), http.StatusBadGateway)
			return
		}

		// Render headers
		for k, tmpl := range step.headerTmpls {
			var hBuf bytes.Buffer
			if err := tmpl.Execute(&hBuf, sctx); err != nil {
				cancel()
				sh.stepErrors[i].Add(1)
				sh.totalErrors.Add(1)
				http.Error(w, fmt.Sprintf("step %d: header template error for %s", i, k), http.StatusBadGateway)
				return
			}
			stepReq.Header.Set(k, hBuf.String())
		}

		// Execute
		resp, err := sh.transport.RoundTrip(stepReq)
		cancel()

		elapsed := time.Since(start)
		sh.stepLatencies[i].Add(elapsed.Microseconds())

		if err != nil {
			sh.stepErrors[i].Add(1)
			sh.totalErrors.Add(1)
			http.Error(w, fmt.Sprintf("step %d: request failed", i), http.StatusBadGateway)
			return
		}

		// Read response body
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			sh.stepErrors[i].Add(1)
			sh.totalErrors.Add(1)
			http.Error(w, fmt.Sprintf("step %d: failed to read response", i), http.StatusBadGateway)
			return
		}

		// Parse JSON into map
		var parsed map[string]interface{}
		if len(respBody) > 0 {
			if err := json.Unmarshal(respBody, &parsed); err != nil {
				// Not JSON — store as raw string
				parsed = map[string]interface{}{"_raw": string(respBody)}
			}
		}
		sctx.Responses[fmt.Sprintf("Resp%d", i)] = parsed

		// Keep last response for final output
		if i == len(sh.steps)-1 {
			lastResp = resp
			// Write final step's response to client
			for k, vv := range resp.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(resp.StatusCode)
			w.Write(respBody)
		}
	}

	_ = lastResp
}

// Stats returns sequential handler stats.
func (sh *SequentialHandler) Stats() map[string]interface{} {
	steps := make([]map[string]interface{}, len(sh.steps))
	for i := range sh.steps {
		steps[i] = map[string]interface{}{
			"errors":            sh.stepErrors[i].Load(),
			"total_latency_us":  sh.stepLatencies[i].Load(),
		}
	}
	return map[string]interface{}{
		"total_requests": sh.totalRequests.Load(),
		"total_errors":   sh.totalErrors.Load(),
		"steps":          steps,
	}
}

// SequentialByRoute manages per-route sequential handlers.
type SequentialByRoute struct {
	byroute.Manager[*SequentialHandler]
}

// NewSequentialByRoute creates a new per-route sequential handler manager.
func NewSequentialByRoute() *SequentialByRoute {
	return &SequentialByRoute{}
}

// AddRoute adds a sequential handler for a route.
func (m *SequentialByRoute) AddRoute(routeID string, cfg config.SequentialConfig, transport http.RoundTripper) error {
	sh, err := New(cfg, transport)
	if err != nil {
		return err
	}
	m.Add(routeID, sh)
	return nil
}

// GetHandler returns the sequential handler for a route.
func (m *SequentialByRoute) GetHandler(routeID string) *SequentialHandler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route sequential handler stats.
func (m *SequentialByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(sh *SequentialHandler) interface{} { return sh.Stats() })
}
