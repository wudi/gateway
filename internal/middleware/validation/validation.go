package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationMetrics tracks validation counters.
type ValidationMetrics struct {
	RequestsValidated  atomic.Int64
	RequestsFailed     atomic.Int64
	ResponsesValidated atomic.Int64
	ResponsesFailed    atomic.Int64
}

// Snapshot returns a copy of the metrics.
func (m *ValidationMetrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"requests_validated":  m.RequestsValidated.Load(),
		"requests_failed":    m.RequestsFailed.Load(),
		"responses_validated": m.ResponsesValidated.Load(),
		"responses_failed":   m.ResponsesFailed.Load(),
	}
}

// Validator validates request/response bodies against JSON schemas.
type Validator struct {
	enabled        bool
	requestSchema  *jsonschema.Schema
	responseSchema *jsonschema.Schema
	logOnly        bool
	metrics        *ValidationMetrics
}

// New creates a new Validator from config.
func New(cfg config.ValidationConfig) (*Validator, error) {
	v := &Validator{
		enabled: cfg.Enabled,
		logOnly: cfg.LogOnly,
		metrics: &ValidationMetrics{},
	}

	if !cfg.Enabled {
		return v, nil
	}

	var err error
	v.requestSchema, err = compileSchema(cfg.Schema, cfg.SchemaFile)
	if err != nil {
		return nil, fmt.Errorf("request schema: %w", err)
	}

	v.responseSchema, err = compileSchema(cfg.ResponseSchema, cfg.ResponseSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("response schema: %w", err)
	}

	return v, nil
}

// compileSchema compiles a JSON schema from an inline string or file path.
func compileSchema(inline, file string) (*jsonschema.Schema, error) {
	var schemaStr string
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("failed to read schema file: %w", err)
		}
		schemaStr = string(data)
	} else if inline != "" {
		schemaStr = inline
	} else {
		return nil, nil
	}

	var schemaDoc interface{}
	if err := json.Unmarshal([]byte(schemaStr), &schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to parse JSON schema: %w", err)
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", schemaDoc); err != nil {
		return nil, fmt.Errorf("failed to add schema resource: %w", err)
	}

	schema, err := c.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to compile schema: %w", err)
	}

	return schema, nil
}

// IsEnabled returns whether validation is enabled.
func (v *Validator) IsEnabled() bool {
	return v.enabled && (v.requestSchema != nil || v.responseSchema != nil)
}

// HasResponseSchema returns whether response validation is configured.
func (v *Validator) HasResponseSchema() bool {
	return v.responseSchema != nil
}

// IsLogOnly returns whether validation errors should be logged instead of rejected.
func (v *Validator) IsLogOnly() bool {
	return v.logOnly
}

// GetMetrics returns the validation metrics.
func (v *Validator) GetMetrics() *ValidationMetrics {
	return v.metrics
}

// Validate validates the request body against the request schema.
func (v *Validator) Validate(r *http.Request) error {
	if v.requestSchema == nil {
		return nil
	}

	if r.Body == nil {
		v.metrics.RequestsValidated.Add(1)
		// Validate nil against schema — will fail if required fields exist
		err := v.requestSchema.Validate(nil)
		if err != nil {
			v.metrics.RequestsFailed.Add(1)
			return fmt.Errorf("validation failed: %s", err.Error())
		}
		return nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body")
	}
	r.Body.Close()

	// Restore body for downstream handlers
	r.Body = io.NopCloser(bytes.NewReader(body))

	if len(body) == 0 {
		v.metrics.RequestsValidated.Add(1)
		err := v.requestSchema.Validate(nil)
		if err != nil {
			v.metrics.RequestsFailed.Add(1)
			return fmt.Errorf("validation failed: %s", err.Error())
		}
		return nil
	}

	// Only validate JSON content
	ct := r.Header.Get("Content-Type")
	if ct != "" && ct != "application/json" && ct != "application/json; charset=utf-8" {
		return nil
	}

	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		v.metrics.RequestsValidated.Add(1)
		v.metrics.RequestsFailed.Add(1)
		return fmt.Errorf("invalid JSON body: %s", err.Error())
	}

	v.metrics.RequestsValidated.Add(1)
	if err := v.requestSchema.Validate(data); err != nil {
		v.metrics.RequestsFailed.Add(1)
		return fmt.Errorf("validation failed: %s", err.Error())
	}

	return nil
}

// ValidateResponseBody validates response body bytes against the response schema.
func (v *Validator) ValidateResponseBody(body []byte) error {
	if v.responseSchema == nil {
		return nil
	}

	if len(body) == 0 {
		v.metrics.ResponsesValidated.Add(1)
		return nil
	}

	var data interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		v.metrics.ResponsesValidated.Add(1)
		v.metrics.ResponsesFailed.Add(1)
		return fmt.Errorf("invalid JSON response: %s", err.Error())
	}

	v.metrics.ResponsesValidated.Add(1)
	if err := v.responseSchema.Validate(data); err != nil {
		v.metrics.ResponsesFailed.Add(1)
		return fmt.Errorf("response validation failed: %s", err.Error())
	}

	return nil
}

// RejectValidation sends a 400 Bad Request with validation error details.
func RejectValidation(w http.ResponseWriter, err error) {
	errors.ErrBadRequest.WithDetails(err.Error()).WriteJSON(w)
}

// ValidatorByRoute manages validators per route.
type ValidatorByRoute struct {
	validators map[string]*Validator
	mu         sync.RWMutex
}

// NewValidatorByRoute creates a new per-route validator manager.
func NewValidatorByRoute() *ValidatorByRoute {
	return &ValidatorByRoute{
		validators: make(map[string]*Validator),
	}
}

// AddRoute adds a validator for a route.
func (m *ValidatorByRoute) AddRoute(routeID string, cfg config.ValidationConfig) error {
	v, err := New(cfg)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.validators[routeID] = v
	m.mu.Unlock()
	return nil
}

// GetValidator returns the validator for a route.
func (m *ValidatorByRoute) GetValidator(routeID string) *Validator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.validators[routeID]
}

// RouteIDs returns all route IDs with validators.
func (m *ValidatorByRoute) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.validators))
	for id := range m.validators {
		ids = append(ids, id)
	}
	return ids
}

// Stats returns per-route validation metrics.
func (m *ValidatorByRoute) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]interface{})
	for id, v := range m.validators {
		result[id] = map[string]interface{}{
			"enabled":             v.enabled,
			"has_request_schema":  v.requestSchema != nil,
			"has_response_schema": v.responseSchema != nil,
			"log_only":            v.logOnly,
			"metrics":             v.metrics.Snapshot(),
		}
	}
	return result
}
