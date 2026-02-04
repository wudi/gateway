package validation

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/example/gateway/internal/config"
)

func TestValidatorRequiredFields(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"required": ["name", "email"],
			"properties": {
				"name": {"type": "string"},
				"email": {"type": "string"}
			}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid body", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"name":"John","email":"john@example.com"}`)))
		r.Header.Set("Content-Type", "application/json")

		if err := v.Validate(r); err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Verify body was restored
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("body should be restored after validation")
		}
	})

	t.Run("missing required field", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"name":"John"}`)))
		r.Header.Set("Content-Type", "application/json")

		err := v.Validate(r)
		if err == nil {
			t.Fatal("expected validation error")
		}

		if err.Error() != "missing required field: email" {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty body with required fields", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(``)))
		r.Header.Set("Content-Type", "application/json")

		err := v.Validate(r)
		if err == nil {
			t.Fatal("expected validation error for empty body")
		}
	})
}

func TestValidatorFieldTypes(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"properties": {
				"age": {"type": "integer"},
				"name": {"type": "string"},
				"active": {"type": "boolean"}
			}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("correct types", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"age":25,"name":"John","active":true}`)))
		r.Header.Set("Content-Type", "application/json")

		if err := v.Validate(r); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"age":"twenty"}`)))
		r.Header.Set("Content-Type", "application/json")

		err := v.Validate(r)
		if err == nil {
			t.Fatal("expected validation error for wrong type")
		}
	})
}

func TestValidatorAdditionalProperties(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"properties": {
				"name": {"type": "string"}
			},
			"additionalProperties": false
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"name":"John","extra":"field"}`)))
	r.Header.Set("Content-Type", "application/json")

	err = v.Validate(r)
	if err == nil {
		t.Fatal("expected validation error for additional property")
	}
}

func TestValidatorDisabled(t *testing.T) {
	v, _ := New(config.ValidationConfig{Enabled: false})

	r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`invalid json`)))

	if err := v.Validate(r); err != nil {
		t.Errorf("disabled validator should not validate: %v", err)
	}
}

func TestValidatorByRoute(t *testing.T) {
	m := NewValidatorByRoute()

	err := m.AddRoute("route1", config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"required": ["name"],
			"properties": {"name": {"type": "string"}}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	v := m.GetValidator("route1")
	if v == nil || !v.IsEnabled() {
		t.Fatal("expected validator for route1")
	}

	if m.GetValidator("unknown") != nil {
		t.Error("expected nil for unknown route")
	}
}
