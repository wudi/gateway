package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/variables"
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
