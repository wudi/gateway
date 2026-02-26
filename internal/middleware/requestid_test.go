package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/variables"
)

func TestRequestID(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check that request ID is in context
		varCtx := variables.GetFromRequest(r)
		if varCtx.RequestID == "" {
			t.Error("Request ID should be set in context")
		}
		w.WriteHeader(http.StatusOK)
	})

	requestID := RequestID()
	final := requestID(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	// Check response header
	if rr.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header should be set in response")
	}
}

func TestRequestIDTrusted(t *testing.T) {
	existingID := "existing-request-id"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.GetFromRequest(r)
		if varCtx.RequestID != existingID {
			t.Errorf("Expected request ID %s, got %s", existingID, varCtx.RequestID)
		}
		w.WriteHeader(http.StatusOK)
	})

	cfg := RequestIDConfig{
		Header:      "X-Request-ID",
		TrustHeader: true,
		Generator:   defaultIDGenerator,
	}

	requestID := RequestIDWithConfig(cfg)
	final := requestID(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", existingID)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Header().Get("X-Request-ID") != existingID {
		t.Errorf("Expected response header %s, got %s", existingID, rr.Header().Get("X-Request-ID"))
	}
}

func TestRequestIDNotTrusted(t *testing.T) {
	existingID := "existing-request-id"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.GetFromRequest(r)
		if varCtx.RequestID == existingID {
			t.Error("Should not trust incoming request ID")
		}
		if varCtx.RequestID == "" {
			t.Error("Should generate new request ID")
		}
		w.WriteHeader(http.StatusOK)
	})

	cfg := RequestIDConfig{
		Header:      "X-Request-ID",
		TrustHeader: false, // Don't trust incoming
		Generator:   defaultIDGenerator,
	}

	requestID := RequestIDWithConfig(cfg)
	final := requestID(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", existingID)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	responseID := rr.Header().Get("X-Request-ID")
	if responseID == existingID {
		t.Error("Should not use incoming request ID when not trusted")
	}
	if responseID == "" {
		t.Error("Should generate new request ID")
	}
}

func TestRequestIDCustomGenerator(t *testing.T) {
	customID := "custom-generated-id"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		varCtx := variables.GetFromRequest(r)
		if varCtx.RequestID != customID {
			t.Errorf("Expected custom ID %s, got %s", customID, varCtx.RequestID)
		}
		w.WriteHeader(http.StatusOK)
	})

	cfg := RequestIDConfig{
		Header: "X-Request-ID",
		Generator: func() string {
			return customID
		},
	}

	requestID := RequestIDWithConfig(cfg)
	final := requestID(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Header().Get("X-Request-ID") != customID {
		t.Errorf("Expected custom ID in response, got %s", rr.Header().Get("X-Request-ID"))
	}
}

func TestGetRequestID(t *testing.T) {
	testID := "test-request-id-123"

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := GetRequestID(r)
		if id != testID {
			t.Errorf("Expected %s, got %s", testID, id)
		}
		w.WriteHeader(http.StatusOK)
	})

	cfg := RequestIDConfig{
		Header:      "X-Request-ID",
		TrustHeader: true,
	}

	requestID := RequestIDWithConfig(cfg)
	final := requestID(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", testID)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)
}

func TestWithRequestID(t *testing.T) {
	ctx := WithRequestID(t.Context(), "my-req-id")

	// Verify we can extract it back via the requestIDKey.
	if id, ok := ctx.Value(requestIDKey{}).(string); !ok || id != "my-req-id" {
		t.Errorf("expected 'my-req-id', got %q (ok=%v)", id, ok)
	}
}

func TestRequestIDFromContext(t *testing.T) {
	t.Run("from requestIDKey", func(t *testing.T) {
		ctx := WithRequestID(t.Context(), "key-id-1")
		id := RequestIDFromContext(ctx)
		if id != "key-id-1" {
			t.Errorf("expected 'key-id-1', got %q", id)
		}
	})

	t.Run("from variable context", func(t *testing.T) {
		varCtx := &variables.Context{RequestID: "var-id-2"}
		ctx := t.Context()
		ctx = context.WithValue(ctx, variables.RequestContextKey{}, varCtx)
		id := RequestIDFromContext(ctx)
		if id != "var-id-2" {
			t.Errorf("expected 'var-id-2', got %q", id)
		}
	})

	t.Run("empty context returns empty string", func(t *testing.T) {
		id := RequestIDFromContext(t.Context())
		if id != "" {
			t.Errorf("expected empty string, got %q", id)
		}
	})
}

func TestRequestIDWithConfigDefaults(t *testing.T) {
	// Pass zero-value config to exercise the default Header and Generator paths.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cfg := RequestIDConfig{
		Header:    "",  // should default to "X-Request-ID"
		Generator: nil, // should default to defaultIDGenerator
	}

	mw := RequestIDWithConfig(cfg)
	final := mw(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	got := rr.Header().Get("X-Request-ID")
	if got == "" {
		t.Error("expected X-Request-ID to be set via default generator")
	}
}
