package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/errors"
)

// Validator validates request bodies against JSON schemas
type Validator struct {
	enabled    bool
	schema     map[string]interface{}
	requiredFields []string
	fieldTypes     map[string]string
}

// New creates a new Validator from config
func New(cfg config.ValidationConfig) (*Validator, error) {
	v := &Validator{
		enabled:    cfg.Enabled,
		fieldTypes: make(map[string]string),
	}

	if !cfg.Enabled {
		return v, nil
	}

	var schemaStr string
	if cfg.SchemaFile != "" {
		data, err := os.ReadFile(cfg.SchemaFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read schema file: %w", err)
		}
		schemaStr = string(data)
	} else if cfg.Schema != "" {
		schemaStr = cfg.Schema
	} else {
		return v, nil
	}

	var schema map[string]interface{}
	if err := json.Unmarshal([]byte(schemaStr), &schema); err != nil {
		return nil, fmt.Errorf("failed to parse JSON schema: %w", err)
	}
	v.schema = schema

	// Extract required fields
	if req, ok := schema["required"].([]interface{}); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				v.requiredFields = append(v.requiredFields, s)
			}
		}
	}

	// Extract property types
	if props, ok := schema["properties"].(map[string]interface{}); ok {
		for name, prop := range props {
			if propMap, ok := prop.(map[string]interface{}); ok {
				if t, ok := propMap["type"].(string); ok {
					v.fieldTypes[name] = t
				}
			}
		}
	}

	return v, nil
}

// IsEnabled returns whether validation is enabled
func (v *Validator) IsEnabled() bool {
	return v.enabled && v.schema != nil
}

// Validate validates the request body against the schema
func (v *Validator) Validate(r *http.Request) error {
	if !v.IsEnabled() {
		return nil
	}

	if r.Body == nil {
		if len(v.requiredFields) > 0 {
			return fmt.Errorf("request body is required")
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
		if len(v.requiredFields) > 0 {
			return fmt.Errorf("request body is required")
		}
		return nil
	}

	// Only validate JSON content
	ct := r.Header.Get("Content-Type")
	if ct != "" && ct != "application/json" && ct != "application/json; charset=utf-8" {
		return nil
	}

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("invalid JSON body: %s", err.Error())
	}

	// Check required fields
	for _, field := range v.requiredFields {
		if _, ok := data[field]; !ok {
			return fmt.Errorf("missing required field: %s", field)
		}
	}

	// Check field types
	for name, expectedType := range v.fieldTypes {
		val, ok := data[name]
		if !ok {
			continue
		}
		if err := checkType(name, val, expectedType); err != nil {
			return err
		}
	}

	// Check additional properties if additionalProperties is false
	if additionalProps, ok := v.schema["additionalProperties"]; ok {
		if ap, ok := additionalProps.(bool); ok && !ap {
			props := v.schema["properties"]
			if propsMap, ok := props.(map[string]interface{}); ok {
				for key := range data {
					if _, ok := propsMap[key]; !ok {
						return fmt.Errorf("additional property not allowed: %s", key)
					}
				}
			}
		}
	}

	return nil
}

func checkType(name string, val interface{}, expectedType string) error {
	switch expectedType {
	case "string":
		if _, ok := val.(string); !ok {
			return fmt.Errorf("field %s must be a string", name)
		}
	case "number":
		if _, ok := val.(float64); !ok {
			return fmt.Errorf("field %s must be a number", name)
		}
	case "integer":
		f, ok := val.(float64)
		if !ok {
			return fmt.Errorf("field %s must be an integer", name)
		}
		if f != float64(int64(f)) {
			return fmt.Errorf("field %s must be an integer", name)
		}
	case "boolean":
		if _, ok := val.(bool); !ok {
			return fmt.Errorf("field %s must be a boolean", name)
		}
	case "array":
		if _, ok := val.([]interface{}); !ok {
			return fmt.Errorf("field %s must be an array", name)
		}
	case "object":
		if _, ok := val.(map[string]interface{}); !ok {
			return fmt.Errorf("field %s must be an object", name)
		}
	}
	return nil
}

// RejectValidation sends a 400 Bad Request with validation error details
func RejectValidation(w http.ResponseWriter, err error) {
	errors.ErrBadRequest.WithDetails(err.Error()).WriteJSON(w)
}

// ValidatorByRoute manages validators per route
type ValidatorByRoute struct {
	validators map[string]*Validator
	mu         sync.RWMutex
}

// NewValidatorByRoute creates a new per-route validator manager
func NewValidatorByRoute() *ValidatorByRoute {
	return &ValidatorByRoute{
		validators: make(map[string]*Validator),
	}
}

// AddRoute adds a validator for a route
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

// GetValidator returns the validator for a route
func (m *ValidatorByRoute) GetValidator(routeID string) *Validator {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.validators[routeID]
}
