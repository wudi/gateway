package errors

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNew(t *testing.T) {
	e := New(400, "bad request")
	if e.Code != 400 {
		t.Errorf("Code = %d, want 400", e.Code)
	}
	if e.Message != "bad request" {
		t.Errorf("Message = %q, want %q", e.Message, "bad request")
	}
	if e.Error() != "bad request" {
		t.Errorf("Error() = %q, want %q", e.Error(), "bad request")
	}
}

func TestWrap(t *testing.T) {
	inner := fmt.Errorf("connection refused")
	e := Wrap(inner, 502, "upstream error")

	if e.Code != 502 {
		t.Errorf("Code = %d, want 502", e.Code)
	}
	if e.Message != "upstream error" {
		t.Errorf("Message = %q, want %q", e.Message, "upstream error")
	}

	want := "upstream error: connection refused"
	if e.Error() != want {
		t.Errorf("Error() = %q, want %q", e.Error(), want)
	}
}

func TestUnwrap(t *testing.T) {
	inner := fmt.Errorf("root cause")
	e := Wrap(inner, 500, "wrapped")

	if e.Unwrap() != inner {
		t.Error("Unwrap should return the underlying error")
	}

	// errors.Is should work through the chain
	if !errors.Is(e, inner) {
		t.Error("errors.Is should find the underlying error")
	}
}

func TestUnwrapNil(t *testing.T) {
	e := New(404, "not found")
	if e.Unwrap() != nil {
		t.Error("Unwrap on a non-wrapped error should return nil")
	}
}

func TestWithDetails(t *testing.T) {
	e := New(400, "Bad Request").WithDetails("field 'name' is required")

	if e.Details != "field 'name' is required" {
		t.Errorf("Details = %q, want %q", e.Details, "field 'name' is required")
	}
	if e.Code != 400 {
		t.Errorf("Code = %d, want 400", e.Code)
	}
	if e.Message != "Bad Request" {
		t.Errorf("Message = %q, want %q", e.Message, "Bad Request")
	}
}

func TestWithRequestID(t *testing.T) {
	e := New(500, "Internal Server Error").WithRequestID("req-123")

	if e.RequestID != "req-123" {
		t.Errorf("RequestID = %q, want %q", e.RequestID, "req-123")
	}
	if e.Code != 500 {
		t.Errorf("Code = %d, want 500", e.Code)
	}
}

func TestWithDetailsAndRequestID(t *testing.T) {
	e := New(400, "Bad Request").
		WithDetails("missing field").
		WithRequestID("req-456")

	if e.Details != "missing field" {
		t.Errorf("Details = %q, want %q", e.Details, "missing field")
	}
	if e.RequestID != "req-456" {
		t.Errorf("RequestID = %q, want %q", e.RequestID, "req-456")
	}
}

func TestWithDetailsPreservesUnderlying(t *testing.T) {
	inner := fmt.Errorf("root cause")
	e := Wrap(inner, 500, "wrapped").WithDetails("extra info")

	if e.Unwrap() != inner {
		t.Error("WithDetails should preserve underlying error")
	}
}

func TestWithRequestIDPreservesFields(t *testing.T) {
	e := New(400, "Bad Request").
		WithDetails("details here").
		WithRequestID("req-789")

	if e.Details != "details here" {
		t.Errorf("WithRequestID should preserve Details, got %q", e.Details)
	}
}

func TestIsRunwayError(t *testing.T) {
	t.Run("RunwayError", func(t *testing.T) {
		e := New(404, "Not Found")
		ge, ok := IsRunwayError(e)
		if !ok {
			t.Fatal("IsRunwayError should return true for RunwayError")
		}
		if ge.Code != 404 {
			t.Errorf("Code = %d, want 404", ge.Code)
		}
	})

	t.Run("regular error", func(t *testing.T) {
		e := fmt.Errorf("regular error")
		_, ok := IsRunwayError(e)
		if ok {
			t.Error("IsRunwayError should return false for regular error")
		}
	})

	t.Run("nil", func(t *testing.T) {
		_, ok := IsRunwayError(nil)
		if ok {
			t.Error("IsRunwayError should return false for nil")
		}
	})
}

func TestWriteJSON_PreSerialized(t *testing.T) {
	singletons := []*RunwayError{
		ErrNotFound, ErrMethodNotAllowed, ErrUnauthorized, ErrForbidden,
		ErrTooManyRequests, ErrBadGateway, ErrServiceUnavailable,
		ErrGatewayTimeout, ErrBadRequest, ErrInternalServer,
		ErrRequestEntityTooLarge,
	}

	for _, e := range singletons {
		t.Run(e.Message, func(t *testing.T) {
			w := httptest.NewRecorder()
			e.WriteJSON(w)

			if ct := w.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/json")
			}
			if w.Code != e.Code {
				t.Errorf("status = %d, want %d", w.Code, e.Code)
			}

			var body map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if int(body["code"].(float64)) != e.Code {
				t.Errorf("body code = %v, want %d", body["code"], e.Code)
			}
			if body["message"] != e.Message {
				t.Errorf("body message = %v, want %q", body["message"], e.Message)
			}
		})
	}
}

func TestWriteJSON_WithDetails(t *testing.T) {
	e := ErrBadRequest.WithDetails("missing field 'name'").WithRequestID("req-abc")

	w := httptest.NewRecorder()
	e.WriteJSON(w)

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if body["details"] != "missing field 'name'" {
		t.Errorf("body details = %v, want %q", body["details"], "missing field 'name'")
	}
	if body["request_id"] != "req-abc" {
		t.Errorf("body request_id = %v, want %q", body["request_id"], "req-abc")
	}
}

func TestSingletonCodes(t *testing.T) {
	tests := []struct {
		err      *RunwayError
		wantCode int
		wantMsg  string
	}{
		{ErrNotFound, 404, "Not Found"},
		{ErrMethodNotAllowed, 405, "Method Not Allowed"},
		{ErrUnauthorized, 401, "Unauthorized"},
		{ErrForbidden, 403, "Forbidden"},
		{ErrTooManyRequests, 429, "Too Many Requests"},
		{ErrBadGateway, 502, "Bad Gateway"},
		{ErrServiceUnavailable, 503, "Service Unavailable"},
		{ErrGatewayTimeout, 504, "Gateway Timeout"},
		{ErrBadRequest, 400, "Bad Request"},
		{ErrInternalServer, 500, "Internal Server Error"},
		{ErrRequestEntityTooLarge, 413, "Request Entity Too Large"},
	}

	for _, tt := range tests {
		t.Run(tt.wantMsg, func(t *testing.T) {
			if tt.err.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", tt.err.Code, tt.wantCode)
			}
			if tt.err.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", tt.err.Message, tt.wantMsg)
			}
		})
	}
}

func TestPreSerializedCount(t *testing.T) {
	if len(preSerialized) != 11 {
		t.Errorf("preSerialized has %d entries, want 11", len(preSerialized))
	}
}

func TestErrorInterface(t *testing.T) {
	var _ error = New(500, "test")
	var _ error = Wrap(fmt.Errorf("inner"), 500, "test")
}
