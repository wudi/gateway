package openapi

import (
	"fmt"
	"sync"

	"github.com/example/gateway/internal/config"
	"github.com/getkin/kin-openapi/openapi3"
)

// OpenAPIStatus describes the validation state of a route.
type OpenAPIStatus struct {
	SpecFile         string         `json:"spec_file,omitempty"`
	SpecID           string         `json:"spec_id,omitempty"`
	OperationID      string         `json:"operation_id,omitempty"`
	ValidateRequest  bool           `json:"validate_request"`
	ValidateResponse bool           `json:"validate_response"`
	LogOnly          bool           `json:"log_only"`
	Metrics          map[string]int64 `json:"metrics"`
}

// OpenAPIByRoute manages per-route OpenAPI validators with a shared spec cache.
type OpenAPIByRoute struct {
	validators map[string]*CompiledOpenAPI
	specCache  map[string]*openapi3.T
	mu         sync.RWMutex
}

// NewOpenAPIByRoute creates a new per-route OpenAPI manager.
func NewOpenAPIByRoute() *OpenAPIByRoute {
	return &OpenAPIByRoute{
		validators: make(map[string]*CompiledOpenAPI),
		specCache:  make(map[string]*openapi3.T),
	}
}

// AddRoute adds an OpenAPI validator for a route.
func (m *OpenAPIByRoute) AddRoute(routeID string, cfg config.OpenAPIRouteConfig) error {
	specFile := cfg.SpecFile
	if specFile == "" {
		return fmt.Errorf("spec_file is required for route %s", routeID)
	}

	doc, err := m.loadSpec(specFile)
	if err != nil {
		return fmt.Errorf("route %s: %w", routeID, err)
	}

	validateReq := true
	if cfg.ValidateRequest != nil {
		validateReq = *cfg.ValidateRequest
	}

	var compiled *CompiledOpenAPI
	if cfg.OperationID != "" {
		compiled, err = NewFromOperationID(doc, cfg.OperationID, validateReq, cfg.ValidateResponse, cfg.LogOnly)
	} else {
		return fmt.Errorf("route %s: operation_id is required when spec_file is set", routeID)
	}
	if err != nil {
		return fmt.Errorf("route %s: %w", routeID, err)
	}

	m.mu.Lock()
	m.validators[routeID] = compiled
	m.mu.Unlock()
	return nil
}

// AddRouteWithDoc adds an OpenAPI validator for a route using a pre-parsed spec document.
// Used for auto-generated routes where the spec is already loaded.
func (m *OpenAPIByRoute) AddRouteWithDoc(routeID, path, method string, doc *openapi3.T, validateRequest, validateResponse, logOnly bool) error {
	compiled, err := New(doc, path, method, validateRequest, validateResponse, logOnly)
	if err != nil {
		return fmt.Errorf("route %s: %w", routeID, err)
	}

	m.mu.Lock()
	m.validators[routeID] = compiled
	m.mu.Unlock()
	return nil
}

// GetValidator returns the OpenAPI validator for a route.
func (m *OpenAPIByRoute) GetValidator(routeID string) *CompiledOpenAPI {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.validators[routeID]
}

// RouteIDs returns all route IDs with OpenAPI validators.
func (m *OpenAPIByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.validators))
	for id := range m.validators {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route OpenAPI validation status.
func (m *OpenAPIByRoute) Stats() map[string]OpenAPIStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]OpenAPIStatus)
	for id, v := range m.validators {
		result[id] = OpenAPIStatus{
			ValidateRequest:  v.validateRequest,
			ValidateResponse: v.validateResponse,
			LogOnly:          v.logOnly,
			Metrics:          v.metrics.Snapshot(),
		}
	}
	return result
}

// loadSpec loads a spec from cache or disk.
func (m *OpenAPIByRoute) loadSpec(file string) (*openapi3.T, error) {
	m.mu.RLock()
	if doc, ok := m.specCache[file]; ok {
		m.mu.RUnlock()
		return doc, nil
	}
	m.mu.RUnlock()

	doc, err := LoadSpec(file)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.specCache[file] = doc
	m.mu.Unlock()

	return doc, nil
}
