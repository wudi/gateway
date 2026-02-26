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

func TestRecoveryWithoutStack(t *testing.T) {
	var loggedStack []byte
	var logCalled bool

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("no-stack panic")
	})

	cfg := RecoveryConfig{
		PrintStack: false,
		LogFunc: func(err interface{}, stack []byte) {
			logCalled = true
			loggedStack = stack
		},
	}

	recovery := RecoveryWithConfig(cfg)
	final := recovery(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if !logCalled {
		t.Error("LogFunc should have been called")
	}
	if len(loggedStack) != 0 {
		t.Errorf("Expected empty stack trace when PrintStack=false, got %d bytes", len(loggedStack))
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", rr.Code)
	}
}

func TestRecoveryWithWriter(t *testing.T) {
	var logMsg string
	var logArgs []interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("writer panic")
	})

	logFunc := func(format string, args ...interface{}) {
		logMsg = format
		logArgs = args
	}

	recovery := RecoveryWithWriter(logFunc)
	final := recovery(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	final.ServeHTTP(rr, req)

	if logMsg == "" {
		t.Error("Expected log function to be called")
	}
	if len(logArgs) == 0 {
		t.Error("Expected log args to be non-empty")
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", rr.Code)
	}
}

func TestRecoveryNilLogFunc(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("nil-log panic")
	})

	cfg := RecoveryConfig{
		PrintStack: false,
		LogFunc:    nil,
	}

	recovery := RecoveryWithConfig(cfg)
	final := recovery(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rr := httptest.NewRecorder()

	// Should not panic even with nil LogFunc.
	final.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", rr.Code)
	}
}

func TestRecoveryWithRequestIDHeader(t *testing.T) {
	var loggedErr interface{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set the X-Request-ID on the response writer before panic,
		// simulating the RequestID middleware running before recovery.
		w.Header().Set("X-Request-ID", "req-abc-123")
		panic("id panic")
	})

	cfg := RecoveryConfig{
		PrintStack: true,
		LogFunc: func(err interface{}, stack []byte) {
			loggedErr = err
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
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("Expected 500, got %d", rr.Code)
	}
}
