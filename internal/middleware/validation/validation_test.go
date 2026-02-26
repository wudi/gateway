package validation

import (
	"bytes"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
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

func TestValidatorMinLength(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"properties": {
				"name": {"type": "string", "minLength": 3}
			}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("too short", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"name":"ab"}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err == nil {
			t.Fatal("expected validation error for minLength")
		}
	})

	t.Run("long enough", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"name":"abc"}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestValidatorPattern(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"properties": {
				"email": {"type": "string", "pattern": "^[a-z]+@[a-z]+\\.[a-z]+$"}
			}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("matches pattern", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"email":"test@example.com"}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("does not match", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"email":"not-an-email"}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err == nil {
			t.Fatal("expected validation error for pattern mismatch")
		}
	})
}

func TestValidatorEnum(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"properties": {
				"color": {"type": "string", "enum": ["red", "green", "blue"]}
			}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid enum", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"color":"red"}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid enum", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"color":"yellow"}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err == nil {
			t.Fatal("expected validation error for invalid enum")
		}
	})
}

func TestValidatorResponseBody(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema:  `{"type": "object"}`,
		ResponseSchema: `{
			"type": "object",
			"required": ["id"],
			"properties": {
				"id": {"type": "integer"}
			}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !v.HasResponseSchema() {
		t.Fatal("expected response schema")
	}

	t.Run("valid response", func(t *testing.T) {
		err := v.ValidateResponseBody([]byte(`{"id":1}`))
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid response", func(t *testing.T) {
		err := v.ValidateResponseBody([]byte(`{"name":"no-id"}`))
		if err == nil {
			t.Fatal("expected response validation error")
		}
	})

	t.Run("empty response", func(t *testing.T) {
		err := v.ValidateResponseBody([]byte{})
		if err != nil {
			t.Errorf("unexpected error for empty response: %v", err)
		}
	})
}

func TestValidatorLogOnly(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		LogOnly: true,
		Schema: `{
			"type": "object",
			"required": ["name"]
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !v.IsLogOnly() {
		t.Fatal("expected log_only to be true")
	}
}

func TestValidatorMetrics(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"required": ["name"],
			"properties": {"name": {"type": "string"}}
		}`,
		ResponseSchema: `{
			"type": "object",
			"required": ["id"]
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Successful request validation
	r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"name":"test"}`)))
	r.Header.Set("Content-Type", "application/json")
	v.Validate(r)

	// Failed request validation
	r2 := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{}`)))
	r2.Header.Set("Content-Type", "application/json")
	v.Validate(r2)

	// Response validations
	v.ValidateResponseBody([]byte(`{"id":1}`))
	v.ValidateResponseBody([]byte(`{}`))

	m := v.GetMetrics().Snapshot()
	if m["requests_validated"] != 2 {
		t.Errorf("expected 2 requests_validated, got %d", m["requests_validated"])
	}
	if m["requests_failed"] != 1 {
		t.Errorf("expected 1 requests_failed, got %d", m["requests_failed"])
	}
	if m["responses_validated"] != 2 {
		t.Errorf("expected 2 responses_validated, got %d", m["responses_validated"])
	}
	if m["responses_failed"] != 1 {
		t.Errorf("expected 1 responses_failed, got %d", m["responses_failed"])
	}
}

func TestValidatorNestedObjects(t *testing.T) {
	v, err := New(config.ValidationConfig{
		Enabled: true,
		Schema: `{
			"type": "object",
			"properties": {
				"address": {
					"type": "object",
					"required": ["city"],
					"properties": {
						"city": {"type": "string"},
						"zip": {"type": "string"}
					}
				}
			}
		}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid nested", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"address":{"city":"NYC","zip":"10001"}}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing nested required", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/", bytes.NewReader([]byte(`{"address":{"zip":"10001"}}`)))
		r.Header.Set("Content-Type", "application/json")
		if err := v.Validate(r); err == nil {
			t.Fatal("expected error for missing nested required field")
		}
	})
}
