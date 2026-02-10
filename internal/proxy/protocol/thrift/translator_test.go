package thrift

import (
	"testing"
)

func TestResolveMethodFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/getUser", "getUser"},
		{"/api/thrift/getUser", "getUser"},
		{"/api/v1/thrift/CreateUser", "CreateUser"},
		{"/singleMethod", "singleMethod"},
		{"/", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := resolveMethodFromPath(tt.path)
			if result != tt.expected {
				t.Errorf("resolveMethodFromPath(%q) = %q, want %q", tt.path, result, tt.expected)
			}
		})
	}
}

func TestTranslatorName(t *testing.T) {
	tr := New()
	if tr.Name() != "http_to_thrift" {
		t.Errorf("Name() = %q, want 'http_to_thrift'", tr.Name())
	}
}

func TestTranslatorMetricsNonexistent(t *testing.T) {
	tr := New()
	m := tr.Metrics("nonexistent-route")
	if m != nil {
		t.Error("expected nil metrics for nonexistent route")
	}
}

func TestTranslatorClose(t *testing.T) {
	tr := New()
	// Close a non-existent route should not error.
	if err := tr.Close("nonexistent"); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}
