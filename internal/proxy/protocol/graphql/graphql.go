package graphql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"text/template"

	"github.com/wudi/runway/internal/byroute"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/tmplutil"
	"github.com/wudi/runway/variables"
)

// Handler translates REST requests into GraphQL queries.
type Handler struct {
	url           string
	queryTmpl     *template.Template
	operationName string
	queryType     string // "query" or "mutation"
	varTmpls      map[string]*template.Template
	transport     http.RoundTripper

	totalRequests atomic.Int64
	totalErrors   atomic.Int64
}

// New creates a GraphQL handler from config.
func New(cfg config.GraphQLProtocolConfig, transport http.RoundTripper) (*Handler, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("graphql: url is required")
	}
	if cfg.Query == "" {
		return nil, fmt.Errorf("graphql: query is required")
	}

	fm := tmplutil.FuncMap()

	queryTmpl, err := template.New("graphql_query").Funcs(fm).Parse(cfg.Query)
	if err != nil {
		return nil, fmt.Errorf("graphql: invalid query template: %w", err)
	}

	queryType := cfg.Type
	if queryType == "" {
		queryType = "query"
	}

	varTmpls := make(map[string]*template.Template, len(cfg.Variables))
	for k, v := range cfg.Variables {
		vt, err := template.New("graphql_var_" + k).Funcs(fm).Parse(v)
		if err != nil {
			return nil, fmt.Errorf("graphql: invalid variable template %s: %w", k, err)
		}
		varTmpls[k] = vt
	}

	return &Handler{
		url:           cfg.URL,
		queryTmpl:     queryTmpl,
		operationName: cfg.OperationName,
		queryType:     queryType,
		varTmpls:      varTmpls,
		transport:     transport,
	}, nil
}

type templateContext struct {
	Method     string
	Path       string
	Host       string
	PathParams map[string]string
	Query      map[string][]string
	Headers    http.Header
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

	// Render query
	var queryBuf bytes.Buffer
	if err := h.queryTmpl.Execute(&queryBuf, ctx); err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "graphql: query template error", http.StatusBadGateway)
		return
	}

	// Render variables
	vars := make(map[string]interface{}, len(h.varTmpls))
	for k, tmpl := range h.varTmpls {
		var vBuf bytes.Buffer
		if err := tmpl.Execute(&vBuf, ctx); err != nil {
			continue
		}
		vars[k] = vBuf.String()
	}

	// Build GraphQL request body
	gqlReq := map[string]interface{}{
		"query":     queryBuf.String(),
		"variables": vars,
	}
	if h.operationName != "" {
		gqlReq["operationName"] = h.operationName
	}

	body, err := json.Marshal(gqlReq)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "graphql: marshal error", http.StatusBadGateway)
		return
	}

	// Create and send request
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "graphql: request creation error", http.StatusBadGateway)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.transport.RoundTrip(req)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "graphql: request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		h.totalErrors.Add(1)
		http.Error(w, "graphql: read response failed", http.StatusBadGateway)
		return
	}

	// Extract "data" field if present
	var gqlResp map[string]json.RawMessage
	if err := json.Unmarshal(respBody, &gqlResp); err == nil {
		if data, ok := gqlResp["data"]; ok {
			respBody = data
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(respBody)
}

// Stats returns handler stats.
func (h *Handler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"total_requests": h.totalRequests.Load(),
		"total_errors":   h.totalErrors.Load(),
		"url":            h.url,
		"operation_name": h.operationName,
		"query_type":     h.queryType,
	}
}

// GraphQLByRoute manages per-route GraphQL handlers.
type GraphQLByRoute struct {
	byroute.Manager[*Handler]
}

// NewGraphQLByRoute creates a new manager.
func NewGraphQLByRoute() *GraphQLByRoute {
	return &GraphQLByRoute{}
}

// AddRoute adds a GraphQL handler for a route.
func (m *GraphQLByRoute) AddRoute(routeID string, cfg config.GraphQLProtocolConfig, transport http.RoundTripper) error {
	h, err := New(cfg, transport)
	if err != nil {
		return err
	}
	m.Add(routeID, h)
	return nil
}

// GetHandler returns the handler for a route.
func (m *GraphQLByRoute) GetHandler(routeID string) *Handler {
	v, _ := m.Get(routeID)
	return v
}

// Stats returns per-route stats.
func (m *GraphQLByRoute) Stats() map[string]interface{} {
	return byroute.CollectStats(&m.Manager, func(h *Handler) interface{} { return h.Stats() })
}
