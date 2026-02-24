package extauth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestExtAuth_HTTPAllow(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth-User", "testuser")
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled: true,
		URL:     authServer.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	result, err := ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allowed {
		t.Error("expected allowed")
	}
	if result.HeadersToInject["X-Auth-User"] != "testuser" {
		t.Errorf("expected X-Auth-User header, got %v", result.HeadersToInject)
	}
}

func TestExtAuth_HTTPDeny(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Error-Code", "AUTH_FAILED")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"access denied"}`))
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled: true,
		URL:     authServer.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	result, err := ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Allowed {
		t.Error("expected denied")
	}
	if result.DeniedStatus != http.StatusForbidden {
		t.Errorf("expected 403, got %d", result.DeniedStatus)
	}
	if string(result.DeniedBody) != `{"error":"access denied"}` {
		t.Errorf("unexpected body: %s", result.DeniedBody)
	}
}

func TestExtAuth_HeaderInjection(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth-User", "alice")
		w.Header().Set("X-Auth-Roles", "admin,user")
		w.Header().Set("X-Internal", "secret")
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled:         true,
		URL:             authServer.URL,
		Timeout:         5 * time.Second,
		HeadersToInject: []string{"X-Auth-User", "X-Auth-Roles"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	result, err := ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Allowed {
		t.Fatal("expected allowed")
	}
	if result.HeadersToInject["X-Auth-User"] != "alice" {
		t.Errorf("expected X-Auth-User=alice, got %v", result.HeadersToInject["X-Auth-User"])
	}
	if result.HeadersToInject["X-Auth-Roles"] != "admin,user" {
		t.Errorf("expected X-Auth-Roles=admin,user, got %v", result.HeadersToInject["X-Auth-Roles"])
	}
	// X-Internal should NOT be injected
	if _, ok := result.HeadersToInject["X-Internal"]; ok {
		t.Error("X-Internal should not be injected")
	}
}

func TestExtAuth_HeadersToSend(t *testing.T) {
	var receivedHeaders http.Header
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture headers received
		var body CheckRequest
		json.NewDecoder(r.Body).Decode(&body)
		receivedHeaders = make(http.Header)
		for k, v := range body.Headers {
			receivedHeaders.Set(k, v)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled:       true,
		URL:           authServer.URL,
		Timeout:       5 * time.Second,
		HeadersToSend: []string{"Authorization", "X-Custom"},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer token123")
	req.Header.Set("X-Custom", "value1")
	req.Header.Set("X-Other", "should-not-be-sent")

	_, err = ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}

	if receivedHeaders.Get("Authorization") != "Bearer token123" {
		t.Error("Authorization header not forwarded")
	}
	if receivedHeaders.Get("X-Custom") != "value1" {
		t.Error("X-Custom header not forwarded")
	}
	if receivedHeaders.Get("X-Other") != "" {
		t.Error("X-Other header should not be forwarded")
	}
}

func TestExtAuth_Timeout(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled: true,
		URL:     authServer.URL,
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	_, err = ea.Check(req)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestExtAuth_FailOpen(t *testing.T) {
	// Use a closed server to simulate unreachable auth service
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled:  true,
		URL:      authServer.URL,
		Timeout:  100 * time.Millisecond,
		FailOpen: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	result, err := ea.Check(req)
	if err != nil {
		t.Fatal("fail_open should not return error")
	}
	if !result.Allowed {
		t.Error("fail_open should allow request")
	}
}

func TestExtAuth_FailClosed(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled:  true,
		URL:      authServer.URL,
		Timeout:  100 * time.Millisecond,
		FailOpen: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	_, err = ea.Check(req)
	if err == nil {
		t.Error("fail_closed should return error")
	}
}

func TestExtAuth_Cache(t *testing.T) {
	var callCount atomic.Int32
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.Header().Set("X-Auth-User", "cached-user")
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled:  true,
		URL:      authServer.URL,
		Timeout:  5 * time.Second,
		CacheTTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer test")

	// First call — hits auth server
	result1, err := ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}
	if !result1.Allowed {
		t.Fatal("expected allowed")
	}

	// Second call — should hit cache
	result2, err := ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}
	if !result2.Allowed {
		t.Fatal("expected allowed from cache")
	}

	if callCount.Load() != 1 {
		t.Errorf("expected auth server called once, got %d", callCount.Load())
	}

	// Verify cache hit metric
	snap := ea.metrics.Snapshot()
	if snap.CacheHits != 1 {
		t.Errorf("expected 1 cache hit, got %d", snap.CacheHits)
	}
}

func TestExtAuth_CacheExpiry(t *testing.T) {
	var callCount atomic.Int32
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled:  true,
		URL:      authServer.URL,
		Timeout:  5 * time.Second,
		CacheTTL: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)

	// First call
	ea.Check(req)
	if callCount.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", callCount.Load())
	}

	// Wait for cache to expire
	time.Sleep(100 * time.Millisecond)

	// Second call — cache expired, should hit auth server again
	ea.Check(req)
	if callCount.Load() != 2 {
		t.Errorf("expected 2 calls after cache expiry, got %d", callCount.Load())
	}
}

func TestExtAuth_Metrics(t *testing.T) {
	var toggle atomic.Bool
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if toggle.Load() {
			w.WriteHeader(http.StatusForbidden)
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled: true,
		URL:     authServer.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)

	// Allow
	ea.Check(req)
	// Deny
	toggle.Store(true)
	ea.Check(req)

	snap := ea.metrics.Snapshot()
	if snap.Total != 2 {
		t.Errorf("expected total=2, got %d", snap.Total)
	}
	if snap.Allowed != 1 {
		t.Errorf("expected allowed=1, got %d", snap.Allowed)
	}
	if snap.Denied != 1 {
		t.Errorf("expected denied=1, got %d", snap.Denied)
	}
}

func TestExtAuth_DeniedBodyPropagated(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("custom unauthorized body"))
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled: true,
		URL:     authServer.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	result, err := ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Allowed {
		t.Error("expected denied")
	}
	if result.DeniedStatus != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", result.DeniedStatus)
	}
	if string(result.DeniedBody) != "custom unauthorized body" {
		t.Errorf("unexpected body: %s", result.DeniedBody)
	}
}

func TestExtAuth_SendsCheckRequestBody(t *testing.T) {
	var receivedReq CheckRequest
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&receivedReq)
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	ea, err := New(config.ExtAuthConfig{
		Enabled: true,
		URL:     authServer.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/users", nil)
	req.Header.Set("Authorization", "Bearer abc")

	_, err = ea.Check(req)
	if err != nil {
		t.Fatal(err)
	}

	if receivedReq.Method != "POST" {
		t.Errorf("expected POST, got %s", receivedReq.Method)
	}
	if receivedReq.Path != "/api/users" {
		t.Errorf("expected /api/users, got %s", receivedReq.Path)
	}
	if receivedReq.Headers["Authorization"] != "Bearer abc" {
		t.Errorf("expected Authorization header in body, got %v", receivedReq.Headers)
	}
}

func TestExtAuthByRoute(t *testing.T) {
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer authServer.Close()

	mgr := NewExtAuthByRoute()

	err := mgr.AddRoute("route1", config.ExtAuthConfig{
		Enabled: true,
		URL:     authServer.URL,
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	if ea := mgr.GetAuth("route1"); ea == nil {
		t.Error("expected ext auth for route1")
	}
	if ea := mgr.GetAuth("route2"); ea != nil {
		t.Error("expected nil for route2")
	}

	ids := mgr.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := mgr.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}
