package mirror

import (
	"strings"

	"github.com/wudi/gateway/config"
)

// DiffConfig holds compiled ignore sets for detailed diff comparison.
// Created once per Mirror in New().
type DiffConfig struct {
	ignoreHeaders    map[string]bool
	ignoreJSONFields []string
	maxBodyCapture   int64
}

const defaultMaxBodyCapture = 1 << 20 // 1 MiB

// alwaysIgnoredHeaders are headers that are always excluded from comparison.
var alwaysIgnoredHeaders = []string{"date", "x-request-id"}

// NewDiffConfig creates a DiffConfig from the compare config.
func NewDiffConfig(cfg config.MirrorCompareConfig) *DiffConfig {
	dc := &DiffConfig{
		ignoreHeaders:    make(map[string]bool),
		ignoreJSONFields: cfg.IgnoreJSONFields,
		maxBodyCapture:   cfg.MaxBodyCapture,
	}
	if dc.maxBodyCapture <= 0 {
		dc.maxBodyCapture = defaultMaxBodyCapture
	}

	for _, h := range alwaysIgnoredHeaders {
		dc.ignoreHeaders[h] = true
	}
	for _, h := range cfg.IgnoreHeaders {
		dc.ignoreHeaders[strings.ToLower(h)] = true
	}

	return dc
}

// StatusDiff describes a status code mismatch.
type StatusDiff struct {
	PrimaryStatus int `json:"primary_status"`
	MirrorStatus  int `json:"mirror_status"`
}

// HeaderDiff describes a single header difference.
type HeaderDiff struct {
	Header       string `json:"header"`
	PrimaryValue string `json:"primary_value"`
	MirrorValue  string `json:"mirror_value"`
}

// BodyDiff describes a body difference.
type BodyDiff struct {
	Type    string                 `json:"type"`
	Details map[string]interface{} `json:"details,omitempty"`
}

// DiffDetail holds the full detail of differences between primary and mirror responses.
type DiffDetail struct {
	StatusDiff  *StatusDiff  `json:"status_diff,omitempty"`
	HeaderDiffs []HeaderDiff `json:"header_diffs,omitempty"`
	BodyDiffs   []BodyDiff   `json:"body_diffs,omitempty"`
}

// DiffTypes returns the types of differences found.
func (d *DiffDetail) DiffTypes() []string {
	var types []string
	if d.StatusDiff != nil {
		types = append(types, "status")
	}
	if len(d.HeaderDiffs) > 0 {
		types = append(types, "headers")
	}
	if len(d.BodyDiffs) > 0 {
		types = append(types, "body")
	}
	return types
}

// HasDiffs returns true if any differences were found.
func (d *DiffDetail) HasDiffs() bool {
	return d.StatusDiff != nil || len(d.HeaderDiffs) > 0 || len(d.BodyDiffs) > 0
}
