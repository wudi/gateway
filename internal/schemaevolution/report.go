package schemaevolution

import "time"

// BreakingChange describes a single incompatible change between spec versions.
type BreakingChange struct {
	Type    string `json:"type"`    // e.g., "endpoint_removed", "required_param_added"
	Path    string `json:"path"`    // API path affected
	Method  string `json:"method"`  // HTTP method if applicable
	Detail  string `json:"detail"`  // human-readable description
}

// CompatibilityReport summarizes compatibility between two spec versions.
type CompatibilityReport struct {
	SpecID          string           `json:"spec_id"`
	OldVersion      string           `json:"old_version"`
	NewVersion      string           `json:"new_version"`
	Compatible      bool             `json:"compatible"`
	BreakingChanges []BreakingChange `json:"breaking_changes,omitempty"`
	Timestamp       time.Time        `json:"timestamp"`
}
