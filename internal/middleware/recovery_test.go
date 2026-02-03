package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRecovery(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	recovery := Recovery()
	final := recovery(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// Should not panic
	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", rr.Code)
	}
}

func TestRecoveryWithConfig(t *testing.T) {
	var loggedErr interface{}
	var loggedStack []byte

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("custom panic")
	})

	cfg := RecoveryConfig{
		PrintStack: true,
		LogFunc: func(err interface{}, stack []byte) {
			loggedErr = err
			loggedStack = stack
		},
	}

	recovery := RecoveryWithConfig(cfg)
	final := recovery(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if loggedErr == nil {
		t.Error("Expected error to be logged")
	}

	if loggedErr != "custom panic" {
		t.Errorf("Expected 'custom panic', got %v", loggedErr)
	}

	if len(loggedStack) == 0 {
		t.Error("Expected stack trace to be captured")
	}
}

func TestRecoveryNoPanic(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})

	recovery := Recovery()
	final := recovery(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("Expected 200, got %d", rr.Code)
	}

	if rr.Body.String() != "success" {
		t.Errorf("Expected 'success', got %s", rr.Body.String())
	}
}
