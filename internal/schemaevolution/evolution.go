package schemaevolution

import (
	"fmt"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/wudi/runway/config"
	"go.uber.org/zap"
)

// Checker compares OpenAPI spec versions for breaking changes.
type Checker struct {
	store   *SpecStore
	mode    string // "warn" or "block"
	logger  *zap.Logger
	mu      sync.RWMutex
	reports map[string]*CompatibilityReport
}

// NewChecker creates a new schema evolution checker.
func NewChecker(cfg config.SchemaEvolutionConfig, logger *zap.Logger) (*Checker, error) {
	mode := cfg.Mode
	if mode == "" {
		mode = "warn"
	}

	storeDir := cfg.StoreDir
	if storeDir == "" {
		storeDir = "/tmp/gw-spec-history"
	}

	store, err := NewSpecStore(storeDir, cfg.MaxVersions)
	if err != nil {
		return nil, fmt.Errorf("create spec store: %w", err)
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &Checker{
		store:   store,
		mode:    mode,
		logger:  logger,
		reports: make(map[string]*CompatibilityReport),
	}, nil
}

// CheckAndStore compares a new spec against the previously stored version,
// then stores the new version. Returns a report (nil if no previous version).
func (c *Checker) CheckAndStore(specID string, newDoc *openapi3.T) (*CompatibilityReport, error) {
	oldDoc, oldVersion, err := c.store.GetPrevious(specID)
	if err != nil {
		c.logger.Warn("schema evolution: failed to load previous spec", zap.String("spec_id", specID), zap.Error(err))
	}

	// Store the new version
	if storeErr := c.store.Store(specID, newDoc); storeErr != nil {
		c.logger.Error("schema evolution: failed to store spec", zap.String("spec_id", specID), zap.Error(storeErr))
	}

	if oldDoc == nil {
		return nil, nil // First version, no comparison
	}

	newVersion := ""
	if newDoc.Info != nil {
		newVersion = newDoc.Info.Version
	}

	changes := detectBreakingChanges(oldDoc, newDoc)
	report := &CompatibilityReport{
		SpecID:          specID,
		OldVersion:      oldVersion,
		NewVersion:      newVersion,
		Compatible:      len(changes) == 0,
		BreakingChanges: changes,
		Timestamp:       time.Now(),
	}

	c.mu.Lock()
	c.reports[specID] = report
	c.mu.Unlock()

	if !report.Compatible {
		for _, ch := range changes {
			c.logger.Warn("schema evolution: breaking change detected",
				zap.String("spec_id", specID),
				zap.String("type", ch.Type),
				zap.String("path", ch.Path),
				zap.String("method", ch.Method),
				zap.String("detail", ch.Detail),
			)
		}

		if c.mode == "block" {
			return report, fmt.Errorf("schema evolution: %d breaking change(s) detected in spec %q", len(changes), specID)
		}
	}

	return report, nil
}

// GetReport returns the most recent compatibility report for a spec.
func (c *Checker) GetReport(specID string) *CompatibilityReport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.reports[specID]
}

// GetAllReports returns all compatibility reports.
func (c *Checker) GetAllReports() map[string]*CompatibilityReport {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string]*CompatibilityReport, len(c.reports))
	for k, v := range c.reports {
		result[k] = v
	}
	return result
}

// detectBreakingChanges compares old and new specs for incompatible changes.
func detectBreakingChanges(oldDoc, newDoc *openapi3.T) []BreakingChange {
	var changes []BreakingChange

	if oldDoc.Paths == nil {
		return changes
	}

	oldPaths := oldDoc.Paths.Map()
	var newPaths map[string]*openapi3.PathItem
	if newDoc.Paths != nil {
		newPaths = newDoc.Paths.Map()
	}

	for path, oldItem := range oldPaths {
		newItem, exists := newPaths[path]
		if !exists {
			changes = append(changes, BreakingChange{
				Type:   "endpoint_removed",
				Path:   path,
				Detail: fmt.Sprintf("endpoint %s was removed", path),
			})
			continue
		}

		// Check each operation
		for method, oldOp := range oldItem.Operations() {
			newOp := newItem.GetOperation(method)
			if newOp == nil {
				changes = append(changes, BreakingChange{
					Type:   "method_removed",
					Path:   path,
					Method: method,
					Detail: fmt.Sprintf("%s %s was removed", method, path),
				})
				continue
			}

			changes = append(changes, checkOperationBreaking(path, method, oldOp, newOp)...)
		}
	}

	return changes
}

func checkOperationBreaking(path, method string, oldOp, newOp *openapi3.Operation) []BreakingChange {
	var changes []BreakingChange

	// Check for new required parameters
	oldParams := buildParamMap(oldOp.Parameters)
	for _, p := range newOp.Parameters {
		if p.Value == nil {
			continue
		}
		if p.Value.Required {
			if _, existed := oldParams[p.Value.Name+":"+p.Value.In]; !existed {
				changes = append(changes, BreakingChange{
					Type:   "required_param_added",
					Path:   path,
					Method: method,
					Detail: fmt.Sprintf("required parameter %q (%s) was added", p.Value.Name, p.Value.In),
				})
			}
		}
	}

	// Check for parameter type changes
	for _, newP := range newOp.Parameters {
		if newP.Value == nil || newP.Value.Schema == nil || newP.Value.Schema.Value == nil {
			continue
		}
		key := newP.Value.Name + ":" + newP.Value.In
		if oldP, ok := oldParams[key]; ok && oldP.Schema != nil && oldP.Schema.Value != nil {
			oldType := schemaTypeString(oldP.Schema.Value)
			newType := schemaTypeString(newP.Value.Schema.Value)
			if oldType != newType {
				changes = append(changes, BreakingChange{
					Type:   "param_type_changed",
					Path:   path,
					Method: method,
					Detail: fmt.Sprintf("parameter %q type changed from %s to %s", newP.Value.Name, oldType, newType),
				})
			}

			// Check enum values removed
			changes = append(changes, checkEnumRemoved(path, method, newP.Value.Name, oldP.Schema.Value, newP.Value.Schema.Value)...)
		}
	}

	// Check request body for new required fields
	if newOp.RequestBody != nil && newOp.RequestBody.Value != nil {
		for _, mt := range newOp.RequestBody.Value.Content {
			if mt.Schema != nil && mt.Schema.Value != nil {
				oldSchema := getRequestBodySchema(oldOp)
				if oldSchema != nil {
					changes = append(changes, checkNewRequiredFields(path, method, "request", oldSchema, mt.Schema.Value)...)
				}
			}
		}
	}

	// Check response body for removed required fields
	if oldOp.Responses != nil && newOp.Responses != nil {
		for code, oldResp := range oldOp.Responses.Map() {
			newResp := newOp.Responses.Value(code)
			if newResp == nil || oldResp.Value == nil || newResp.Value == nil {
				continue
			}
			for ct, oldMT := range oldResp.Value.Content {
				if newMT, ok := newResp.Value.Content[ct]; ok {
					if oldMT.Schema != nil && oldMT.Schema.Value != nil && newMT.Schema != nil && newMT.Schema.Value != nil {
						changes = append(changes, checkRemovedRequiredFields(path, method, code, oldMT.Schema.Value, newMT.Schema.Value)...)
					}
				}
			}
		}
	}

	return changes
}

func buildParamMap(params openapi3.Parameters) map[string]*openapi3.Parameter {
	m := make(map[string]*openapi3.Parameter)
	for _, p := range params {
		if p.Value != nil {
			m[p.Value.Name+":"+p.Value.In] = p.Value
		}
	}
	return m
}

func schemaTypeString(s *openapi3.Schema) string {
	types := s.Type.Slice()
	if len(types) == 0 {
		return ""
	}
	return types[0]
}

func checkEnumRemoved(path, method, paramName string, oldSchema, newSchema *openapi3.Schema) []BreakingChange {
	if len(oldSchema.Enum) == 0 || len(newSchema.Enum) == 0 {
		return nil
	}

	newEnums := make(map[string]bool)
	for _, v := range newSchema.Enum {
		newEnums[fmt.Sprint(v)] = true
	}

	var changes []BreakingChange
	for _, v := range oldSchema.Enum {
		if !newEnums[fmt.Sprint(v)] {
			changes = append(changes, BreakingChange{
				Type:   "enum_value_removed",
				Path:   path,
				Method: method,
				Detail: fmt.Sprintf("enum value %v was removed from parameter %q", v, paramName),
			})
		}
	}
	return changes
}

func getRequestBodySchema(op *openapi3.Operation) *openapi3.Schema {
	if op == nil || op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	for _, mt := range op.RequestBody.Value.Content {
		if mt.Schema != nil && mt.Schema.Value != nil {
			return mt.Schema.Value
		}
	}
	return nil
}

func checkNewRequiredFields(path, method, phase string, oldSchema, newSchema *openapi3.Schema) []BreakingChange {
	var changes []BreakingChange

	oldRequired := make(map[string]bool)
	for _, r := range oldSchema.Required {
		oldRequired[r] = true
	}

	for _, r := range newSchema.Required {
		if !oldRequired[r] {
			// Check if the field existed at all before
			if _, existed := oldSchema.Properties[r]; !existed {
				changes = append(changes, BreakingChange{
					Type:   "required_field_added",
					Path:   path,
					Method: method,
					Detail: fmt.Sprintf("required %s body field %q was added", phase, r),
				})
			}
		}
	}
	return changes
}

func checkRemovedRequiredFields(path, method, statusCode string, oldSchema, newSchema *openapi3.Schema) []BreakingChange {
	var changes []BreakingChange

	// Check if any required field from old response is missing in new
	for _, r := range oldSchema.Required {
		if _, exists := newSchema.Properties[r]; !exists {
			changes = append(changes, BreakingChange{
				Type:   "required_response_field_removed",
				Path:   path,
				Method: method,
				Detail: fmt.Sprintf("required response field %q was removed from %s response", r, statusCode),
			})
		}
	}
	return changes
}
