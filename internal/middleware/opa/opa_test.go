package opa

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/variables"
)

// withVarContext attaches a variables.Context to the request so
// variables.GetFromRequest returns it.
func withVarContext(r *http.Request, vc *variables.Context) *http.Request {
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, vc)
	return r.WithContext(ctx)
}

func TestOPAEnforcer_AllowRequest(t *testing.T) {
	var calls atomic.Int64
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// Verify the request body has the expected shape
		body, _ := io.ReadAll(r.Body)
		var input opaInput
		if err := json.Unmarshal(body, &input); err != nil {
			t.Errorf("failed to unmarshal OPA input: %v", err)
		}
		if input.Input.Method != "GET" {
			t.Errorf("expected method GET, got %s", input.Input.Method)
		}
		if input.Input.Path != "/api/data" {
			t.Errorf("expected path /api/data, got %s", input.Input.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(opaResponse{Result: true})
	}))
	defer opaServer.Close()

	enforcer, err := New(config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
		Timeout:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create enforcer: %v", err)
	}

	backendCalled := false
	handler := enforcer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !backendCalled {
		t.Error("expected backend to be called")
	}
	if calls.Load() != 1 {
		t.Errorf("expected 1 OPA call, got %d", calls.Load())
	}
	if enforcer.TotalRequests() != 1 {
		t.Errorf("expected 1 total request, got %d", enforcer.TotalRequests())
	}
	if enforcer.TotalDenied() != 0 {
		t.Errorf("expected 0 denied, got %d", enforcer.TotalDenied())
	}
}

func TestOPAEnforcer_DenyRequest(t *testing.T) {
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(opaResponse{Result: false})
	}))
	defer opaServer.Close()

	enforcer, err := New(config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
	})
	if err != nil {
		t.Fatalf("failed to create enforcer: %v", err)
	}

	backendCalled := false
	handler := enforcer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/admin/delete", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rec.Code)
	}
	if backendCalled {
		t.Error("backend should not be called on deny")
	}
	if enforcer.TotalDenied() != 1 {
		t.Errorf("expected 1 denied, got %d", enforcer.TotalDenied())
	}
}

func TestOPAEnforcer_FailOpenOnError(t *testing.T) {
	// OPA server that returns 500
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer opaServer.Close()

	enforcer, err := New(config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
		FailOpen:   true,
	})
	if err != nil {
		t.Fatalf("failed to create enforcer: %v", err)
	}

	backendCalled := false
	handler := enforcer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 (fail open), got %d", rec.Code)
	}
	if !backendCalled {
		t.Error("expected backend to be called (fail open)")
	}
	if enforcer.TotalErrors() != 1 {
		t.Errorf("expected 1 error, got %d", enforcer.TotalErrors())
	}
}

func TestOPAEnforcer_FailClosedOnError(t *testing.T) {
	// OPA server that returns 500
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer opaServer.Close()

	enforcer, err := New(config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
		FailOpen:   false,
	})
	if err != nil {
		t.Fatalf("failed to create enforcer: %v", err)
	}

	backendCalled := false
	handler := enforcer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 (fail closed), got %d", rec.Code)
	}
	if backendCalled {
		t.Error("backend should not be called (fail closed)")
	}
	if enforcer.TotalErrors() != 1 {
		t.Errorf("expected 1 error, got %d", enforcer.TotalErrors())
	}
}

func TestOPAEnforcer_CacheHit(t *testing.T) {
	var calls atomic.Int64
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(opaResponse{Result: true})
	}))
	defer opaServer.Close()

	enforcer, err := New(config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
		CacheTTL:   1 * time.Minute,
	})
	if err != nil {
		t.Fatalf("failed to create enforcer: %v", err)
	}

	handler := enforcer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request - should call OPA
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/api/data", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", rec1.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("first request: expected 1 OPA call, got %d", calls.Load())
	}

	// Second request with same key - should use cache
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/api/data", nil)
	req2.RemoteAddr = "10.0.0.1:5678"
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("second request: expected 200, got %d", rec2.Code)
	}
	if calls.Load() != 1 {
		t.Errorf("second request: expected still 1 OPA call (cache hit), got %d", calls.Load())
	}
	if enforcer.TotalRequests() != 2 {
		t.Errorf("expected 2 total requests, got %d", enforcer.TotalRequests())
	}
}

func TestOPAEnforcer_IdentityPropagation(t *testing.T) {
	var receivedInput opaInput
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedInput)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(opaResponse{Result: true})
	}))
	defer opaServer.Close()

	enforcer, err := New(config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
	})
	if err != nil {
		t.Fatalf("failed to create enforcer: %v", err)
	}

	handler := enforcer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	// Attach identity via variables context
	vc := &variables.Context{
		Request: req,
		Identity: &variables.Identity{
			ClientID: "client-123",
			AuthType: "jwt",
			Claims: map[string]interface{}{
				"sub":  "user-456",
				"role": "admin",
			},
		},
	}
	req = withVarContext(req, vc)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if receivedInput.Input.Identity == nil {
		t.Fatal("expected identity in OPA input")
	}
	if receivedInput.Input.Identity.ClientID != "client-123" {
		t.Errorf("expected client_id client-123, got %s", receivedInput.Input.Identity.ClientID)
	}
	if receivedInput.Input.Identity.AuthType != "jwt" {
		t.Errorf("expected auth_type jwt, got %s", receivedInput.Input.Identity.AuthType)
	}
	if receivedInput.Input.Identity.Claims["sub"] != "user-456" {
		t.Errorf("unexpected claims: %v", receivedInput.Input.Identity.Claims)
	}
}

func TestOPAByRoute(t *testing.T) {
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(opaResponse{Result: true})
	}))
	defer opaServer.Close()

	m := NewOPAByRoute()

	err := m.AddRoute("route1", config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
	})
	if err != nil {
		t.Fatalf("failed to add route: %v", err)
	}

	if m.Lookup("route1") == nil {
		t.Error("expected enforcer for route1")
	}
	if m.Lookup("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}

func TestOPAByRoute_AddRouteError(t *testing.T) {
	m := NewOPAByRoute()

	// Missing URL should return error
	err := m.AddRoute("route1", config.OPAConfig{
		Enabled:    true,
		PolicyPath: "authz/allow",
	})
	if err == nil {
		t.Error("expected error for missing URL")
	}

	// Missing policy_path should return error
	err = m.AddRoute("route2", config.OPAConfig{
		Enabled: true,
		URL:     "http://localhost:8181",
	})
	if err == nil {
		t.Error("expected error for missing policy_path")
	}
}

func TestOPAEnforcer_HeadersFilter(t *testing.T) {
	var receivedInput opaInput
	opaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedInput)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(opaResponse{Result: true})
	}))
	defer opaServer.Close()

	enforcer, err := New(config.OPAConfig{
		Enabled:    true,
		URL:        opaServer.URL,
		PolicyPath: "authz/allow",
		Headers:    []string{"Authorization", "X-Custom"},
	})
	if err != nil {
		t.Fatalf("failed to create enforcer: %v", err)
	}

	handler := enforcer.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer token123")
	req.Header.Set("X-Custom", "value")
	req.Header.Set("X-Other", "should-not-be-sent")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	// Only the specified headers should be sent
	if receivedInput.Input.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("expected Authorization header, got %v", receivedInput.Input.Headers)
	}
	if receivedInput.Input.Headers["X-Custom"] != "value" {
		t.Errorf("expected X-Custom header, got %v", receivedInput.Input.Headers)
	}
	if _, ok := receivedInput.Input.Headers["X-Other"]; ok {
		t.Error("X-Other header should not be sent when headers filter is configured")
	}
}
