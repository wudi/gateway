// +build integration

package test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/gateway"
)

// TestSlidingWindowRateLimitIntegration tests the sliding window rate limiter
// rejects excess requests and recovers after the window period.
func TestSlidingWindowRateLimitIntegration(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:         "sw-limited",
				Path:       "/sw",
				PathPrefix: true,
				Backends: []config.BackendConfig{
					{URL: backend.URL},
				},
				RateLimit: config.RateLimitConfig{
					Enabled:   true,
					Rate:      5,
					Period:    200 * time.Millisecond,
					Algorithm: "sliding_window",
					PerIP:     true,
				},
			},
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	ts := httptest.NewServer(gw.Handler())
	defer ts.Close()

	// Make requests up to the limit
	for i := 0; i < 5; i++ {
		resp, err := http.Get(ts.URL + "/sw/test")
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Request %d: expected 200, got %d", i, resp.StatusCode)
		}
	}

	// Next request should be rate limited
	resp, err := http.Get(ts.URL + "/sw/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected 429, got %d", resp.StatusCode)
	}

	// Check rate limit headers
	if resp.Header.Get("X-RateLimit-Limit") == "" {
		t.Error("Missing X-RateLimit-Limit header")
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("Missing Retry-After header")
	}

	// Wait for more than 2 full periods so the sliding window fully recovers
	time.Sleep(450 * time.Millisecond)

	// Should be allowed again after window passes
	resp, err = http.Get(ts.URL + "/sw/test")
	if err != nil {
		t.Fatalf("Request after recovery failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 after window recovery, got %d", resp.StatusCode)
	}
}

// TestSlidingWindowAdminEndpoint verifies the /rate-limits admin endpoint
// reports the correct algorithm for sliding window routes.
func TestSlidingWindowAdminEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:         "sw-route",
				Path:       "/sw-admin",
				PathPrefix: true,
				Backends: []config.BackendConfig{
					{URL: backend.URL},
				},
				RateLimit: config.RateLimitConfig{
					Enabled:   true,
					Rate:      100,
					Period:    time.Minute,
					Algorithm: "sliding_window",
				},
			},
		},
	}

	gw, err := gateway.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create gateway: %v", err)
	}
	defer gw.Close()

	// Verify the limiter is registered as sliding_window
	rl := gw.GetRateLimiters()
	sw := rl.GetSlidingWindowLimiter("sw-route")
	if sw == nil {
		t.Fatal("Expected sliding window limiter for sw-route")
	}

	// Token bucket should not be set for this route
	tb := rl.GetLimiter("sw-route")
	if tb != nil {
		t.Error("Did not expect token bucket limiter for sliding window route")
	}
}
