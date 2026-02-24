package aggregate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/wudi/gateway/internal/byroute"
	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/middleware/backendenc"
	"github.com/wudi/gateway/internal/middleware/transform"
	"github.com/wudi/gateway/internal/tmplutil"
	"github.com/wudi/gateway/variables"
)

// compiledBackend holds pre-compiled templates for one aggregate backend.
type compiledBackend struct {
	name       string
	urlTmpl    *template.Template
	method     string
	headerTmpl map[string]*template.Template
	group      string
	required   bool
	timeout    time.Duration
	variables  map[string]string
	encoding   string                          // per-backend encoding (xml, yaml, etc.)
	transform  *transform.CompiledBodyTransform // per-backend response transform
}

// AggregateHandler fans out requests to multiple backends and merges JSON responses.
type AggregateHandler struct {
	backends          []compiledBackend
	transport         http.RoundTripper
	timeout           time.Duration
	failStrategy      string // "abort" or "partial"
	responseTransform *transform.CompiledBodyTransform // post-merge transform
	completionHeader  bool

	totalRequests atomic.Int64
	totalErrors   atomic.Int64
	backendErrors []atomic.Int64
	backendLatNs  []atomic.Int64
}

// New creates an AggregateHandler from config.
func New(cfg config.AggregateConfig, transport http.RoundTripper) (*AggregateHandler, error) {
	if len(cfg.Backends) < 2 {
		return nil, fmt.Errorf("aggregate requires at least 2 backends, got %d", len(cfg.Backends))
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	failStrategy := cfg.FailStrategy
	if failStrategy == "" {
		failStrategy = "abort"
	}

	funcMap := tmplutil.FuncMap()
	backends := make([]compiledBackend, len(cfg.Backends))
	for i, b := range cfg.Backends {
		urlTmpl, err := template.New(b.Name + "_url").Funcs(funcMap).Parse(b.URL)
		if err != nil {
			return nil, fmt.Errorf("backend %s: invalid URL template: %w", b.Name, err)
		}

		method := b.Method
		if method == "" {
			method = http.MethodGet
		}

		headerTmpls := make(map[string]*template.Template, len(b.Headers))
		for hk, hv := range b.Headers {
			ht, err := template.New(b.Name + "_header_" + hk).Funcs(tmplutil.FuncMap()).Parse(hv)
			if err != nil {
				return nil, fmt.Errorf("backend %s: invalid header template %s: %w", b.Name, hk, err)
			}
			headerTmpls[hk] = ht
		}

		bt := b.Timeout
		if bt == 0 {
			bt = timeout
		}

		cb := compiledBackend{
			name:       b.Name,
			urlTmpl:    urlTmpl,
			method:     method,
			headerTmpl: headerTmpls,
			group:      b.Group,
			required:   b.Required,
			timeout:    bt,
			variables:  b.Variables,
			encoding:   b.Encoding,
		}

		// Compile per-backend transform if configured
		if b.Transform.IsActive() {
			ct, err := transform.NewCompiledBodyTransform(b.Transform)
			if err != nil {
				return nil, fmt.Errorf("backend %s: invalid transform: %w", b.Name, err)
			}
			cb.transform = ct
		}

		backends[i] = cb
	}

	ah := &AggregateHandler{
		backends:      backends,
		transport:     transport,
		timeout:       timeout,
		failStrategy:  failStrategy,
		backendErrors: make([]atomic.Int64, len(backends)),
		backendLatNs:  make([]atomic.Int64, len(backends)),
	}

	// Compile post-merge response transform if configured
	if cfg.ResponseTransform.IsActive() {
		ct, err := transform.NewCompiledBodyTransform(cfg.ResponseTransform)
		if err != nil {
			return nil, fmt.Errorf("response_transform: %w", err)
		}
		ah.responseTransform = ct
	}

	return ah, nil
}

// templateContext builds the data object for templates.
type templateContext struct {
	Method     string
	Path       string
	Host       string
	PathParams map[string]string
	Query      map[string][]string
	Headers    http.Header
	ClientIP   string
	RouteID    string
	Variables  map[string]string // per-backend custom static variables
}

type backendResult struct {
	index int
	name  string
	group string
	body  []byte
	err   error
}

func (ah *AggregateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ah.totalRequests.Add(1)

	varCtx := variables.GetFromRequest(r)

	ctx := templateContext{
		Method:     r.Method,
		Path:       r.URL.Path,
		Host:       r.Host,
		Query:      r.URL.Query(),
		Headers:    r.Header,
		ClientIP:   variables.ExtractClientIP(r),
	}
	if varCtx != nil {
		ctx.PathParams = varCtx.PathParams
		ctx.RouteID = varCtx.RouteID
	}

	results := make(chan backendResult, len(ah.backends))
	var wg sync.WaitGroup

	for i, b := range ah.backends {
		wg.Add(1)
		go func(idx int, backend compiledBackend) {
			defer wg.Done()

			start := time.Now()
			bctx := ctx
			bctx.Variables = backend.variables
			body, err := ah.callBackend(r, backend, bctx)
			ah.backendLatNs[idx].Add(time.Since(start).Nanoseconds())

			if err != nil {
				ah.backendErrors[idx].Add(1)
			}

			results <- backendResult{
				index: idx,
				name:  backend.name,
				group: backend.group,
				body:  body,
				err:   err,
			}
		}(i, b)
	}

	// Close results channel after all goroutines complete
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	collected := make([]backendResult, 0, len(ah.backends))
	for res := range results {
		collected = append(collected, res)
	}

	// Check for errors
	var errors []map[string]interface{}
	hasRequiredFailure := false
	hasAnyFailure := false

	for _, res := range collected {
		if res.err != nil {
			hasAnyFailure = true
			errors = append(errors, map[string]interface{}{
				"backend": res.name,
				"error":   res.err.Error(),
			})
			if ah.backends[res.index].required {
				hasRequiredFailure = true
			}
		}
	}

	// Abort if strategy requires it
	if ah.failStrategy == "abort" && hasAnyFailure {
		ah.totalErrors.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  "aggregate backend failure",
			"errors": errors,
		})
		return
	}

	if hasRequiredFailure {
		ah.totalErrors.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  "required aggregate backend failure",
			"errors": errors,
		})
		return
	}

	// Merge responses
	merged := make(map[string]interface{})

	for _, res := range collected {
		if res.err != nil {
			continue
		}

		if res.group != "" {
			// Wrap under group key
			var parsed interface{}
			if err := json.Unmarshal(res.body, &parsed); err != nil {
				merged[res.group] = json.RawMessage(res.body)
			} else {
				merged[res.group] = parsed
			}
		} else {
			// Merge at top level
			var obj map[string]interface{}
			if err := json.Unmarshal(res.body, &obj); err == nil {
				for k, v := range obj {
					merged[k] = v
				}
			}
		}
	}

	// Add errors array for partial mode
	if ah.failStrategy == "partial" && hasAnyFailure {
		merged["_errors"] = errors
		w.Header().Set("X-Aggregate-Partial", "true")
	}

	// Set completion header
	if ah.completionHeader {
		if hasAnyFailure {
			w.Header().Set("X-Gateway-Completed", "false")
		} else {
			w.Header().Set("X-Gateway-Completed", "true")
		}
	}

	// Serialize merged result
	mergedJSON, _ := json.Marshal(merged)

	// Apply post-merge response transform
	if ah.responseTransform != nil {
		mergedJSON = ah.responseTransform.Transform(mergedJSON, nil)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(mergedJSON)))
	w.WriteHeader(http.StatusOK)
	w.Write(mergedJSON)
}

func (ah *AggregateHandler) callBackend(origReq *http.Request, backend compiledBackend, ctx templateContext) ([]byte, error) {
	// Render URL
	var urlBuf bytes.Buffer
	if err := backend.urlTmpl.Execute(&urlBuf, ctx); err != nil {
		return nil, fmt.Errorf("URL template: %w", err)
	}

	// Create request with timeout context
	reqCtx := origReq.Context()
	if backend.timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, backend.timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, backend.method, urlBuf.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Render headers
	for hk, ht := range backend.headerTmpl {
		var hBuf bytes.Buffer
		if err := ht.Execute(&hBuf, ctx); err != nil {
			continue
		}
		req.Header.Set(hk, hBuf.String())
	}

	resp, err := ah.transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Apply encoding conversion (e.g., XML/YAML to JSON)
	if backend.encoding != "" {
		decoded, decErr := backendenc.DecodeBytes(body, backend.encoding)
		if decErr != nil {
			return nil, fmt.Errorf("decode %s: %w", backend.encoding, decErr)
		}
		body = decoded
	}

	// Apply per-backend transform
	if backend.transform != nil {
		body = backend.transform.Transform(body, nil)
	}

	return body, nil
}

// Stats returns aggregate handler stats.
func (ah *AggregateHandler) Stats() map[string]interface{} {
	backends := make([]map[string]interface{}, len(ah.backends))
	for i := range ah.backends {
		backends[i] = map[string]interface{}{
			"name":            ah.backends[i].name,
			"errors":          ah.backendErrors[i].Load(),
			"total_latency_us": ah.backendLatNs[i].Load() / 1000,
		}
	}
	return map[string]interface{}{
		"total_requests": ah.totalRequests.Load(),
		"total_errors":   ah.totalErrors.Load(),
		"fail_strategy":  ah.failStrategy,
		"backends":       backends,
	}
}

// AggregateByRoute manages per-route aggregate handlers.
type AggregateByRoute struct {
	byroute.Manager[*AggregateHandler]
}

// NewAggregateByRoute creates a new per-route aggregate handler manager.
func NewAggregateByRoute() *AggregateByRoute {
	return &AggregateByRoute{}
}

// AddRoute adds an aggregate handler for a route.
func (m *AggregateByRoute) AddRoute(routeID string, cfg config.AggregateConfig, transport http.RoundTripper, completionHeader ...bool) error {
	ah, err := New(cfg, transport)
	if err != nil {
		return err
	}
	if len(completionHeader) > 0 {
		ah.completionHeader = completionHeader[0]
	}
	m.Add(routeID, ah)
	return nil
}

// GetHandler returns the aggregate handler for a route.
func (m *AggregateByRoute) GetHandler(routeID string) *AggregateHandler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route aggregate handler stats.
func (m *AggregateByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(ah *AggregateHandler) interface{} { return ah.Stats() })
}
