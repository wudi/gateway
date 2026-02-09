package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEchoHandler_GET(t *testing.T) {
	handler := NewEchoHandler("test-route")

	req := httptest.NewRequest(http.MethodGet, "/debug?foo=bar&baz=qux", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Custom", "hello")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var resp echoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Method != "GET" {
		t.Errorf("method: got %q, want GET", resp.Method)
	}
	if resp.Path != "/debug" {
		t.Errorf("path: got %q, want /debug", resp.Path)
	}
	if resp.RouteID != "test-route" {
		t.Errorf("route_id: got %q, want test-route", resp.RouteID)
	}
	if resp.Query["foo"] != "bar" {
		t.Errorf("query foo: got %q, want bar", resp.Query["foo"])
	}
	if resp.Query["baz"] != "qux" {
		t.Errorf("query baz: got %q, want qux", resp.Query["baz"])
	}
	if resp.Headers["Accept"] != "application/json" {
		t.Errorf("header Accept: got %q", resp.Headers["Accept"])
	}
	if resp.Headers["X-Custom"] != "hello" {
		t.Errorf("header X-Custom: got %q", resp.Headers["X-Custom"])
	}
	if resp.Timestamp == "" {
		t.Error("timestamp should not be empty")
	}
	if resp.Body != "" {
		t.Errorf("body should be empty for GET, got %q", resp.Body)
	}
}

func TestEchoHandler_POST(t *testing.T) {
	handler := NewEchoHandler("post-route")

	body := `{"key":"value"}`
	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp echoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Method != "POST" {
		t.Errorf("method: got %q, want POST", resp.Method)
	}
	if resp.Body != body {
		t.Errorf("body: got %q, want %q", resp.Body, body)
	}
}

func TestEchoHandler_QueryFlattened(t *testing.T) {
	handler := NewEchoHandler("flat-route")

	// Multi-value query param — only first value returned
	req := httptest.NewRequest(http.MethodGet, "/test?a=1&a=2&a=3", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var resp echoResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Query["a"] != "1" {
		t.Errorf("query a: got %q, want 1 (first value)", resp.Query["a"])
	}
}

func TestEchoHandler_LargeBody(t *testing.T) {
	handler := NewEchoHandler("large-route")

	// Body larger than 1MB — should be truncated
	largeBody := strings.Repeat("x", echoMaxBodySize+1000)
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(largeBody))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Body)
	var resp echoResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Body) != echoMaxBodySize {
		t.Errorf("body length: got %d, want %d (capped)", len(resp.Body), echoMaxBodySize)
	}
}
