package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func testConfig(url string, events []string) config.WebhooksConfig {
	return config.WebhooksConfig{
		Enabled:   true,
		Timeout:   2 * time.Second,
		Workers:   2,
		QueueSize: 100,
		Retry: config.WebhookRetryConfig{
			MaxRetries: 1,
			Backoff:    10 * time.Millisecond,
			MaxBackoff: 50 * time.Millisecond,
		},
		Endpoints: []config.WebhookEndpoint{
			{
				ID:     "test",
				URL:    url,
				Events: events,
			},
		},
	}
}

func TestDeliveryPayloadAndHeaders(t *testing.T) {
	var received *Event
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header
		body, _ := io.ReadAll(r.Body)
		var evt Event
		json.Unmarshal(body, &evt)
		received = &evt
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	d := NewDispatcher(testConfig(server.URL, []string{"backend.*"}))
	defer d.Close()

	event := NewEvent(BackendUnhealthy, "api-v1", map[string]interface{}{
		"url": "http://backend:8080",
	})
	d.Emit(event)

	time.Sleep(200 * time.Millisecond)

	if received == nil {
		t.Fatal("expected event to be delivered")
	}
	if received.Type != BackendUnhealthy {
		t.Errorf("expected type %s, got %s", BackendUnhealthy, received.Type)
	}
	if received.RouteID != "api-v1" {
		t.Errorf("expected route_id api-v1, got %s", received.RouteID)
	}
	if received.Data["url"] != "http://backend:8080" {
		t.Errorf("expected data url, got %v", received.Data["url"])
	}

	// Check headers
	if headers.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", headers.Get("Content-Type"))
	}
	if headers.Get("X-Webhook-Event") != string(BackendUnhealthy) {
		t.Errorf("expected X-Webhook-Event header, got %s", headers.Get("X-Webhook-Event"))
	}
	if headers.Get("X-Webhook-Timestamp") == "" {
		t.Error("expected X-Webhook-Timestamp header")
	}
}

func TestHMACSignature(t *testing.T) {
	secret := "test-secret-123"
	var receivedBody []byte
	var sigHeader string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHeader = r.Header.Get("X-Webhook-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig(server.URL, []string{"*"})
	cfg.Endpoints[0].Secret = secret
	d := NewDispatcher(cfg)
	defer d.Close()

	d.Emit(NewEvent(ConfigReloadSuccess, "", nil))
	time.Sleep(200 * time.Millisecond)

	if sigHeader == "" {
		t.Fatal("expected X-Webhook-Signature header")
	}

	// Verify HMAC
	expectedPrefix := "sha256="
	if sigHeader[:7] != expectedPrefix {
		t.Fatalf("expected signature prefix %s, got %s", expectedPrefix, sigHeader[:7])
	}
	hexSig := sigHeader[7:]

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if hexSig != expectedSig {
		t.Errorf("HMAC mismatch: got %s, expected %s", hexSig, expectedSig)
	}
}

func TestEventTypeFiltering(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		event    EventType
		match    bool
	}{
		{"exact match", []string{"backend.healthy"}, BackendHealthy, true},
		{"exact no match", []string{"backend.healthy"}, BackendUnhealthy, false},
		{"wildcard canary", []string{"canary.*"}, CanaryStarted, true},
		{"wildcard canary 2", []string{"canary.*"}, CanaryRolledBack, true},
		{"wildcard canary no match", []string{"canary.*"}, ConfigReloadSuccess, false},
		{"star matches all", []string{"*"}, CircuitBreakerStateChange, true},
		{"multiple patterns", []string{"backend.*", "config.*"}, ConfigReloadFailure, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, p := range tt.patterns {
				if matchesPattern(tt.event, p) {
					if !tt.match {
						t.Errorf("pattern %q should NOT match %s", p, tt.event)
					}
					return
				}
			}
			if tt.match {
				t.Errorf("no pattern matched %s, expected match", tt.event)
			}
		})
	}
}

func TestRouteFiltering(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig(server.URL, []string{"*"})
	cfg.Endpoints[0].Routes = []string{"payments-api"}
	d := NewDispatcher(cfg)
	defer d.Close()

	// Should match
	d.Emit(NewEvent(BackendHealthy, "payments-api", nil))
	// Should not match
	d.Emit(NewEvent(BackendHealthy, "users-api", nil))
	// Global event (no route) should still match when routes filter is set
	d.Emit(NewEvent(ConfigReloadSuccess, "", nil))

	time.Sleep(200 * time.Millisecond)

	// Only the payments-api event should match (global event with empty RouteID
	// passes when len(routes) > 0 but event.RouteID is empty)
	count := callCount.Load()
	if count != 2 {
		t.Errorf("expected 2 deliveries (payments-api + global), got %d", count)
	}
}

func TestRetryOn500(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := callCount.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig(server.URL, []string{"*"})
	cfg.Retry.MaxRetries = 3
	cfg.Retry.Backoff = 10 * time.Millisecond
	cfg.Retry.MaxBackoff = 50 * time.Millisecond
	d := NewDispatcher(cfg)
	defer d.Close()

	d.Emit(NewEvent(BackendHealthy, "test", nil))
	time.Sleep(500 * time.Millisecond)

	calls := callCount.Load()
	if calls != 3 {
		t.Errorf("expected 3 attempts (1 original + 2 retries), got %d", calls)
	}

	stats := d.Stats()
	if stats.Metrics.TotalDelivered != 1 {
		t.Errorf("expected 1 delivered, got %d", stats.Metrics.TotalDelivered)
	}
	if stats.Metrics.TotalRetries != 2 {
		t.Errorf("expected 2 retries, got %d", stats.Metrics.TotalRetries)
	}
}

func TestQueueFullDropsEvent(t *testing.T) {
	cfg := config.WebhooksConfig{
		Enabled:   true,
		Timeout:   2 * time.Second,
		Workers:   0, // will default to 4 but we cancel immediately
		QueueSize: 1,
		Endpoints: []config.WebhookEndpoint{
			{ID: "test", URL: "http://localhost:1", Events: []string{"*"}},
		},
	}

	d := NewDispatcher(cfg)

	// Cancel immediately so workers stop consuming
	d.cancel()
	d.wg.Wait()

	// Fill the queue
	d.Emit(NewEvent(BackendHealthy, "", nil))
	// This should be dropped
	d.Emit(NewEvent(BackendHealthy, "", nil))

	if d.metrics.TotalDropped.Load() != 1 {
		t.Errorf("expected 1 dropped, got %d", d.metrics.TotalDropped.Load())
	}
}

func TestUpdateEndpoints(t *testing.T) {
	var callCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig("http://localhost:1", []string{"*"}) // initially unreachable
	cfg.Retry.MaxRetries = 0
	d := NewDispatcher(cfg)
	defer d.Close()

	// Update endpoints to the real server
	d.UpdateEndpoints([]config.WebhookEndpoint{
		{ID: "updated", URL: server.URL, Events: []string{"*"}},
	})

	d.Emit(NewEvent(BackendHealthy, "", nil))
	time.Sleep(200 * time.Millisecond)

	if callCount.Load() != 1 {
		t.Errorf("expected 1 delivery after endpoint update, got %d", callCount.Load())
	}
}

func TestCloseDoesNotPanic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig(server.URL, []string{"*"})
	cfg.Retry.MaxRetries = 0
	d := NewDispatcher(cfg)

	d.Emit(NewEvent(BackendHealthy, "", nil))
	d.Emit(NewEvent(BackendUnhealthy, "", nil))
	time.Sleep(50 * time.Millisecond) // let workers process

	// Close should return without panic or deadlock
	d.Close()
}

func TestStatsReturnsCorrectCounts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig(server.URL, []string{"*"})
	cfg.Retry.MaxRetries = 0
	d := NewDispatcher(cfg)
	defer d.Close()

	d.Emit(NewEvent(BackendHealthy, "", nil))
	d.Emit(NewEvent(BackendUnhealthy, "", nil))
	time.Sleep(200 * time.Millisecond)

	stats := d.Stats()
	if stats.Enabled != true {
		t.Error("expected enabled")
	}
	if stats.Endpoints != 1 {
		t.Errorf("expected 1 endpoint, got %d", stats.Endpoints)
	}
	if stats.Metrics.TotalEmitted != 2 {
		t.Errorf("expected 2 emitted, got %d", stats.Metrics.TotalEmitted)
	}
	if stats.Metrics.TotalDelivered != 2 {
		t.Errorf("expected 2 delivered, got %d", stats.Metrics.TotalDelivered)
	}
	if len(stats.RecentEvents) != 2 {
		t.Errorf("expected 2 recent events, got %d", len(stats.RecentEvents))
	}
}

func TestCustomHeaders(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testConfig(server.URL, []string{"*"})
	cfg.Endpoints[0].Headers = map[string]string{
		"X-Custom-Header": "custom-value",
	}
	d := NewDispatcher(cfg)
	defer d.Close()

	d.Emit(NewEvent(BackendHealthy, "", nil))
	time.Sleep(200 * time.Millisecond)

	if receivedHeaders.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("expected custom header, got %s", receivedHeaders.Get("X-Custom-Header"))
	}
}

func TestSignPayload(t *testing.T) {
	secret := "mysecret"
	payload := []byte(`{"type":"test"}`)

	sig := signPayload(secret, payload)

	// Verify independently
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))

	if sig != expected {
		t.Errorf("signPayload mismatch: got %s, expected %s", sig, expected)
	}
}
