package openapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func loadTestDoc(t *testing.T) {
	t.Helper()
}

func TestValidateRequest(t *testing.T) {
	doc, err := LoadSpec("testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid POST /pets", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "POST", true, false, false)
		if err != nil {
			t.Fatal(err)
		}

		body := `{"name":"Fido"}`
		r := httptest.NewRequest("POST", "/pets", bytes.NewReader([]byte(body)))
		r.Header.Set("Content-Type", "application/json")

		if err := compiled.ValidateRequest(r, nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid POST /pets missing required field", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "POST", true, false, false)
		if err != nil {
			t.Fatal(err)
		}

		body := `{"tag":"dog"}`
		r := httptest.NewRequest("POST", "/pets", bytes.NewReader([]byte(body)))
		r.Header.Set("Content-Type", "application/json")

		if err := compiled.ValidateRequest(r, nil); err == nil {
			t.Fatal("expected validation error for missing required field")
		}
	})

	t.Run("valid GET /pets with query param", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "GET", true, false, false)
		if err != nil {
			t.Fatal(err)
		}

		r := httptest.NewRequest("GET", "/pets?limit=10", nil)
		if err := compiled.ValidateRequest(r, nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid GET /pets query param out of range", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "GET", true, false, false)
		if err != nil {
			t.Fatal(err)
		}

		r := httptest.NewRequest("GET", "/pets?limit=200", nil)
		if err := compiled.ValidateRequest(r, nil); err == nil {
			t.Fatal("expected validation error for out-of-range query param")
		}
	})

	t.Run("GET /pets/{petId} with path params", func(t *testing.T) {
		compiled, err := New(doc, "/pets/123", "GET", true, false, false)
		if err != nil {
			t.Fatal(err)
		}

		r := httptest.NewRequest("GET", "/pets/123", nil)
		if err := compiled.ValidateRequest(r, map[string]string{"petId": "123"}); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestValidateResponse(t *testing.T) {
	doc, err := LoadSpec("testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid response", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "POST", false, true, false)
		if err != nil {
			t.Fatal(err)
		}

		body := `{"id":1,"name":"Fido"}`
		header := http.Header{"Content-Type": []string{"application/json"}}
		r := httptest.NewRequest("POST", "/pets", bytes.NewReader([]byte(`{"name":"Fido"}`)))
		r.Header.Set("Content-Type", "application/json")

		err = compiled.ValidateResponse(201, header, io.NopCloser(bytes.NewReader([]byte(body))), r, nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("invalid response missing required field", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "POST", false, true, false)
		if err != nil {
			t.Fatal(err)
		}

		body := `{"tag":"dog"}`
		header := http.Header{"Content-Type": []string{"application/json"}}
		r := httptest.NewRequest("POST", "/pets", bytes.NewReader([]byte(`{"name":"Fido"}`)))
		r.Header.Set("Content-Type", "application/json")

		err = compiled.ValidateResponse(201, header, io.NopCloser(bytes.NewReader([]byte(body))), r, nil)
		if err == nil {
			t.Fatal("expected response validation error")
		}
	})
}

func TestNewFromOperationID(t *testing.T) {
	doc, err := LoadSpec("testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("found", func(t *testing.T) {
		compiled, err := NewFromOperationID(doc, "listPets", true, false, false)
		if err != nil {
			t.Fatal(err)
		}

		r := httptest.NewRequest("GET", "/pets", nil)
		if err := compiled.ValidateRequest(r, nil); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := NewFromOperationID(doc, "nonexistent", true, false, false)
		if err == nil {
			t.Fatal("expected error for unknown operationId")
		}
	})
}

func TestMetrics(t *testing.T) {
	doc, err := LoadSpec("testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := New(doc, "/pets", "POST", true, true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Successful request
	r1 := httptest.NewRequest("POST", "/pets", bytes.NewReader([]byte(`{"name":"Fido"}`)))
	r1.Header.Set("Content-Type", "application/json")
	compiled.ValidateRequest(r1, nil)

	// Failed request
	r2 := httptest.NewRequest("POST", "/pets", bytes.NewReader([]byte(`{"tag":"x"}`)))
	r2.Header.Set("Content-Type", "application/json")
	compiled.ValidateRequest(r2, nil)

	// Successful response
	header := http.Header{"Content-Type": []string{"application/json"}}
	r3 := httptest.NewRequest("POST", "/pets", bytes.NewReader([]byte(`{"name":"Fido"}`)))
	r3.Header.Set("Content-Type", "application/json")
	compiled.ValidateResponse(201, header, io.NopCloser(bytes.NewReader([]byte(`{"id":1,"name":"Fido"}`))), r3, nil)

	m := compiled.GetMetrics().Snapshot()
	if m["requests_validated"] != 2 {
		t.Errorf("expected 2 requests_validated, got %d", m["requests_validated"])
	}
	if m["requests_failed"] != 1 {
		t.Errorf("expected 1 requests_failed, got %d", m["requests_failed"])
	}
	if m["responses_validated"] != 1 {
		t.Errorf("expected 1 responses_validated, got %d", m["responses_validated"])
	}
}

func TestLoadSpecError(t *testing.T) {
	_, err := LoadSpec("testdata/nonexistent.yaml")
	if err == nil {
		t.Fatal("expected error for missing spec file")
	}
}

func TestValidatesResponseFlag(t *testing.T) {
	doc, err := LoadSpec("testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("response validation disabled", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "GET", true, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if compiled.ValidatesResponse() {
			t.Fatal("expected ValidatesResponse to be false")
		}
	})

	t.Run("response validation enabled", func(t *testing.T) {
		compiled, err := New(doc, "/pets", "GET", true, true, false)
		if err != nil {
			t.Fatal(err)
		}
		if !compiled.ValidatesResponse() {
			t.Fatal("expected ValidatesResponse to be true")
		}
	})
}

func TestIsLogOnly(t *testing.T) {
	doc, err := LoadSpec("testdata/petstore.yaml")
	if err != nil {
		t.Fatal(err)
	}

	compiled, err := New(doc, "/pets", "GET", true, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.IsLogOnly() {
		t.Fatal("expected IsLogOnly to be true")
	}
}
