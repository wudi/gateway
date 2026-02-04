package tracing

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/gateway/internal/config"
)

func TestTracerMiddleware(t *testing.T) {
	tracer := New(config.TracingConfig{
		Enabled:     true,
		ServiceName: "test-gateway",
		SampleRate:  1.0,
	})

	var capturedTraceID string
	handler := tracer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTraceID = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if capturedTraceID == "" {
		t.Error("expected traceparent header to be set")
	}

	// Check format: 00-{32hex}-{16hex}-01
	if len(capturedTraceID) != 55 {
		t.Errorf("traceparent should be 55 chars, got %d", len(capturedTraceID))
	}

	// Response should have X-Trace-ID
	if w.Header().Get("X-Trace-ID") == "" {
		t.Error("expected X-Trace-ID response header")
	}
}

func TestTracerMiddlewarePropagation(t *testing.T) {
	tracer := New(config.TracingConfig{
		Enabled: true,
	})

	existingTrace := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	var capturedTraceID string
	handler := tracer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedTraceID = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("traceparent", existingTrace)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if capturedTraceID != existingTrace {
		t.Errorf("existing traceparent should be preserved, got %s", capturedTraceID)
	}
}

func TestTracerDisabled(t *testing.T) {
	tracer := New(config.TracingConfig{
		Enabled: false,
	})

	handler := tracer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("traceparent") != "" {
			t.Error("disabled tracer should not add traceparent")
		}
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)
}

func TestInjectHeaders(t *testing.T) {
	src := httptest.NewRequest("GET", "/", nil)
	src.Header.Set("traceparent", "00-abc-def-01")
	src.Header.Set("tracestate", "vendor=value")

	dst := httptest.NewRequest("GET", "/", nil)
	InjectHeaders(src, dst)

	if dst.Header.Get("traceparent") != "00-abc-def-01" {
		t.Error("traceparent not propagated")
	}
	if dst.Header.Get("tracestate") != "vendor=value" {
		t.Error("tracestate not propagated")
	}
}

func TestExtractTraceID(t *testing.T) {
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	id := extractTraceID(tp)

	if id != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected trace ID 4bf92f3577b34da6a3ce929d0e0e4736, got %s", id)
	}
}
