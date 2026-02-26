package piiredact

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestRedact_Email(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"email"},
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	input := []byte("Contact us at user@example.com for details")
	output := pr.redact(input)
	if strings.Contains(string(output), "user@example.com") {
		t.Errorf("email was not redacted: %s", output)
	}
	if !strings.Contains(string(output), "****") {
		t.Errorf("expected mask characters in output: %s", output)
	}
}

func TestRedact_CreditCard(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"credit_card"},
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	input := []byte("Card: 4111111111111111")
	output := pr.redact(input)
	if strings.Contains(string(output), "4111111111111111") {
		t.Errorf("credit card was not redacted: %s", output)
	}
}

func TestRedact_SSN(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"ssn"},
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	input := []byte("SSN: 123-45-6789")
	output := pr.redact(input)
	if strings.Contains(string(output), "123-45-6789") {
		t.Errorf("SSN was not redacted: %s", output)
	}
}

func TestRedact_Phone(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"phone"},
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	input := []byte("Call 555-123-4567")
	output := pr.redact(input)
	if strings.Contains(string(output), "555-123-4567") {
		t.Errorf("phone was not redacted: %s", output)
	}
}

func TestRedact_CustomPattern(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled: true,
		Custom: []config.PIIPattern{
			{Name: "api_key", Pattern: `APIKEY-[A-Z0-9]{8}`, Replacement: "[REDACTED]"},
		},
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	input := []byte("Key: APIKEY-AB12CD34")
	output := pr.redact(input)
	if !strings.Contains(string(output), "[REDACTED]") {
		t.Errorf("custom pattern not applied: %s", output)
	}
}

func TestMiddleware_ResponseRedaction(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"email"},
		Scope:    "response",
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	mw := pr.Middleware()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"email":"user@example.com"}`))
	})

	handler := mw(next)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, r)

	body := rr.Body.String()
	if strings.Contains(body, "user@example.com") {
		t.Errorf("email in response body was not redacted: %s", body)
	}
}

func TestMiddleware_RequestRedaction(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"email"},
		Scope:    "request",
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	mw := pr.Middleware()
	var capturedBody string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		capturedBody = string(b)
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"email":"user@example.com"}`))
	r.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rr, r)

	if strings.Contains(capturedBody, "user@example.com") {
		t.Errorf("email in request body was not redacted: %s", capturedBody)
	}
}

func TestMiddleware_HeaderRedaction(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"email"},
		Scope:    "request",
		Headers:  []string{"X-User-Email"},
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	mw := pr.Middleware()
	var capturedHeader string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("X-User-Email")
		w.WriteHeader(http.StatusOK)
	})

	handler := mw(next)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-User-Email", "user@example.com")
	handler.ServeHTTP(rr, r)

	if strings.Contains(capturedHeader, "user@example.com") {
		t.Errorf("email in header was not redacted: %s", capturedHeader)
	}
}

func TestMiddleware_NonTextContentSkipped(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"email"},
		Scope:    "response",
	}

	pr, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	mw := pr.Middleware()
	responseBody := "binary data with user@example.com inside"
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(responseBody))
	})

	handler := mw(next)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rr, r)

	// Non-text content should not be redacted
	if !strings.Contains(rr.Body.String(), "user@example.com") {
		t.Error("non-text content should not be redacted")
	}
}

func TestManager(t *testing.T) {
	m := NewPIIRedactByRoute()

	cfg := config.PIIRedactionConfig{
		Enabled:  true,
		BuiltIns: []string{"email"},
	}

	if err := m.AddRoute("route1", cfg); err != nil {
		t.Fatal(err)
	}

	if pr := m.GetRedactor("route1"); pr == nil {
		t.Error("expected redactor for route1")
	}
	if pr := m.GetRedactor("missing"); pr != nil {
		t.Error("expected nil for missing route")
	}

	stats := m.Stats()
	if stats == nil {
		t.Error("expected non-nil stats")
	}
}

func TestNew_InvalidCustomPattern(t *testing.T) {
	cfg := config.PIIRedactionConfig{
		Enabled: true,
		Custom: []config.PIIPattern{
			{Name: "bad", Pattern: `[invalid`},
		},
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}
