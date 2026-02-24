package tracing

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestTracerMiddleware(t *testing.T) {
	tracer, err := New(config.TracingConfig{
		Enabled:     true,
		ServiceName: "test-gateway",
		SampleRate:  1.0,
		Insecure:    true,
	})
	if err != nil {
		t.Fatalf("failed to create tracer: %v", err)
	}
	defer tracer.Close()

	handler := tracer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	// Response should have X-Trace-ID
	if w.Header().Get("X-Trace-ID") == "" {
		t.Error("expected X-Trace-ID response header")
	}
}

func TestTracerDisabled(t *testing.T) {
	tracer, err := New(config.TracingConfig{
		Enabled: false,
	})
	if err != nil {
		t.Fatalf("failed to create tracer: %v", err)
	}

	called := false
	handler := tracer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if !called {
		t.Error("handler should have been called")
	}

	if w.Header().Get("X-Trace-ID") != "" {
		t.Error("disabled tracer should not add X-Trace-ID")
	}
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

func TestExtractTraceIDFromTraceparent(t *testing.T) {
	tp := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	parts := strings.Split(tp, "-")
	if len(parts) < 3 || parts[1] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("expected trace ID 4bf92f3577b34da6a3ce929d0e0e4736, got %v", parts)
	}
}
