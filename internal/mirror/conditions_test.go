package mirror

import (
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestConditionsMethodFilter(t *testing.T) {
	c, err := NewConditions(config.MirrorConditionsConfig{
		Methods: []string{"POST", "PUT"},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		method string
		want   bool
	}{
		{"GET", false},
		{"POST", true},
		{"PUT", true},
		{"DELETE", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest(tt.method, "/test", nil)
		if got := c.Match(r); got != tt.want {
			t.Errorf("method=%s: got %v, want %v", tt.method, got, tt.want)
		}
	}
}

func TestConditionsHeaderFilter(t *testing.T) {
	c, err := NewConditions(config.MirrorConditionsConfig{
		Headers: map[string]string{"X-Mirror": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Without header
	r := httptest.NewRequest("GET", "/test", nil)
	if c.Match(r) {
		t.Error("should not match without required header")
	}

	// With correct header
	r = httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("X-Mirror", "true")
	if !c.Match(r) {
		t.Error("should match with correct header")
	}

	// With wrong value
	r = httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("X-Mirror", "false")
	if c.Match(r) {
		t.Error("should not match with wrong header value")
	}
}

func TestConditionsPathRegex(t *testing.T) {
	c, err := NewConditions(config.MirrorConditionsConfig{
		PathRegex: `^/api/v[23]/`,
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/users", false},
		{"/api/v2/users", true},
		{"/api/v3/orders", true},
		{"/other", false},
	}

	for _, tt := range tests {
		r := httptest.NewRequest("GET", tt.path, nil)
		if got := c.Match(r); got != tt.want {
			t.Errorf("path=%s: got %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestConditionsANDLogic(t *testing.T) {
	c, err := NewConditions(config.MirrorConditionsConfig{
		Methods:   []string{"POST"},
		PathRegex: `^/api/`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// POST to /api/ — both conditions met
	r := httptest.NewRequest("POST", "/api/users", nil)
	if !c.Match(r) {
		t.Error("should match when all conditions met")
	}

	// GET to /api/ — method fails
	r = httptest.NewRequest("GET", "/api/users", nil)
	if c.Match(r) {
		t.Error("should not match when method condition fails")
	}

	// POST to /other — path fails
	r = httptest.NewRequest("POST", "/other", nil)
	if c.Match(r) {
		t.Error("should not match when path condition fails")
	}
}

func TestConditionsEmpty(t *testing.T) {
	c, err := NewConditions(config.MirrorConditionsConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if !c.IsEmpty() {
		t.Error("empty config should produce empty conditions")
	}

	// Empty conditions match everything
	r := httptest.NewRequest("DELETE", "/anything", nil)
	if !c.Match(r) {
		t.Error("empty conditions should match all requests")
	}
}

func TestConditionsInvalidRegex(t *testing.T) {
	_, err := NewConditions(config.MirrorConditionsConfig{
		PathRegex: "[invalid",
	})
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestConditionsNilReceiver(t *testing.T) {
	var c *Conditions
	r := httptest.NewRequest("GET", "/test", nil)
	if !c.Match(r) {
		t.Error("nil conditions should match all requests")
	}
}
