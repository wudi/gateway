package openapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// OpenAPIMetrics tracks per-route OpenAPI validation counters.
type OpenAPIMetrics struct {
	RequestsValidated  atomic.Int64
	RequestsFailed     atomic.Int64
	ResponsesValidated atomic.Int64
	ResponsesFailed    atomic.Int64
}

// Snapshot returns a copy of the metrics.
func (m *OpenAPIMetrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"requests_validated":  m.RequestsValidated.Load(),
		"requests_failed":    m.RequestsFailed.Load(),
		"responses_validated": m.ResponsesValidated.Load(),
		"responses_failed":   m.ResponsesFailed.Load(),
	}
}

// CompiledOpenAPI holds a pre-built OpenAPI route for validating requests/responses.
type CompiledOpenAPI struct {
	router           routers.Router
	route            *routers.Route
	pathParams       map[string]string
	validateRequest  bool
	validateResponse bool
	logOnly          bool
	metrics          *OpenAPIMetrics
}

// noopAuthFunc skips authentication validation (gateway handles auth separately).
func noopAuthFunc(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	return nil
}

// New creates a CompiledOpenAPI by matching a path+method in the spec document.
func New(doc *openapi3.T, path, method string, validateRequest, validateResponse, logOnly bool) (*CompiledOpenAPI, error) {
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAPI router: %w", err)
	}

	// Build a synthetic request to find the route
	req, err := http.NewRequest(method, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create route-finding request: %w", err)
	}

	route, pathParams, err := router.FindRoute(req)
	if err != nil {
		return nil, fmt.Errorf("no operation found for %s %s: %w", method, path, err)
	}

	return &CompiledOpenAPI{
		router:           router,
		route:            route,
		pathParams:       pathParams,
		validateRequest:  validateRequest,
		validateResponse: validateResponse,
		logOnly:          logOnly,
		metrics:          &OpenAPIMetrics{},
	}, nil
}

// NewFromOperationID creates a CompiledOpenAPI by finding an operation by its operationId.
func NewFromOperationID(doc *openapi3.T, operationID string, validateRequest, validateResponse, logOnly bool) (*CompiledOpenAPI, error) {
	// Find the path and method for the given operationId
	for path, pathItem := range doc.Paths.Map() {
		for method, op := range pathItem.Operations() {
			if op.OperationID == operationID {
				return New(doc, path, method, validateRequest, validateResponse, logOnly)
			}
		}
	}
	return nil, fmt.Errorf("operationId %q not found in spec", operationID)
}

// ValidateRequest validates an HTTP request against the OpenAPI spec.
func (c *CompiledOpenAPI) ValidateRequest(r *http.Request, pathParams map[string]string) error {
	if !c.validateRequest {
		return nil
	}

	// Merge pre-computed path params with runtime ones
	params := c.pathParams
	if len(pathParams) > 0 {
		params = make(map[string]string, len(c.pathParams)+len(pathParams))
		for k, v := range c.pathParams {
			params[k] = v
		}
		for k, v := range pathParams {
			params[k] = v
		}
	}

	input := &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: params,
		Route:      c.route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: noopAuthFunc,
		},
	}

	c.metrics.RequestsValidated.Add(1)
	if err := openapi3filter.ValidateRequest(r.Context(), input); err != nil {
		c.metrics.RequestsFailed.Add(1)
		return err
	}
	return nil
}

// ValidateResponse validates an HTTP response against the OpenAPI spec.
func (c *CompiledOpenAPI) ValidateResponse(status int, header http.Header, body io.ReadCloser, r *http.Request, pathParams map[string]string) error {
	if !c.validateResponse {
		return nil
	}

	params := c.pathParams
	if len(pathParams) > 0 {
		params = make(map[string]string, len(c.pathParams)+len(pathParams))
		for k, v := range c.pathParams {
			params[k] = v
		}
		for k, v := range pathParams {
			params[k] = v
		}
	}

	reqInput := &openapi3filter.RequestValidationInput{
		Request:    r,
		PathParams: params,
		Route:      c.route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: noopAuthFunc,
		},
	}

	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: reqInput,
		Status:                 status,
		Header:                 header,
		Body:                   body,
	}

	c.metrics.ResponsesValidated.Add(1)
	if err := openapi3filter.ValidateResponse(r.Context(), respInput); err != nil {
		c.metrics.ResponsesFailed.Add(1)
		return err
	}
	return nil
}

// ValidatesResponse returns whether response validation is enabled.
func (c *CompiledOpenAPI) ValidatesResponse() bool {
	return c.validateResponse
}

// IsLogOnly returns whether validation errors should be logged instead of rejected.
func (c *CompiledOpenAPI) IsLogOnly() bool {
	return c.logOnly
}

// GetMetrics returns the OpenAPI validation metrics.
func (c *CompiledOpenAPI) GetMetrics() *OpenAPIMetrics {
	return c.metrics
}

// LoadSpec loads and validates an OpenAPI spec from a file.
func LoadSpec(file string) (*openapi3.T, error) {
	ctx := context.Background()
	loader := &openapi3.Loader{Context: ctx, IsExternalRefsAllowed: true}
	doc, err := loader.LoadFromFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}
	if err := doc.Validate(ctx); err != nil {
		return nil, fmt.Errorf("invalid OpenAPI spec: %w", err)
	}
	return doc, nil
}
