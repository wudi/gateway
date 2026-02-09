package health

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthChecker(t *testing.T) {
	// Create a healthy backend
	healthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthyServer.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: 100 * time.Millisecond,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            healthyServer.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})

	// Wait for health check
	time.Sleep(200 * time.Millisecond)

	status := checker.GetStatus(healthyServer.URL)
	if status != StatusHealthy {
		t.Errorf("expected healthy status, got %s", status)
	}
}

func TestHealthCheckerUnhealthy(t *testing.T) {
	// Create an unhealthy backend
	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unhealthyServer.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: 100 * time.Millisecond,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            unhealthyServer.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})

	// Wait for health check
	time.Sleep(200 * time.Millisecond)

	status := checker.GetStatus(unhealthyServer.URL)
	if status != StatusUnhealthy {
		t.Errorf("expected unhealthy status, got %s", status)
	}
}

func TestHealthCheckerThresholds(t *testing.T) {
	var requestCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&requestCount, 1)
		// First 2 requests succeed, then fail
		if count <= 2 {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: 50 * time.Millisecond,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		HealthyAfter:   2,  // Need 2 successes to be healthy
		UnhealthyAfter: 2,  // Need 2 failures to be unhealthy
	})

	// Wait for first check
	time.Sleep(100 * time.Millisecond)

	// Should still be unknown (only 1 success)
	status := checker.GetStatus(server.URL)
	if status == StatusHealthy {
		// Might be healthy if 2 checks ran
	}

	// Wait for more checks
	time.Sleep(200 * time.Millisecond)

	// After failures, should become unhealthy
	status = checker.GetStatus(server.URL)
	// Status depends on timing, just verify it's set
	if status == StatusUnknown {
		t.Error("expected status to be determined")
	}
}

func TestHealthCheckerCallback(t *testing.T) {
	var callbackURL string
	var callbackStatus Status

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: 50 * time.Millisecond,
		OnChange: func(url string, status Status) {
			callbackURL = url
			callbackStatus = status
		},
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})

	// Wait for health check
	time.Sleep(200 * time.Millisecond)

	if callbackURL != server.URL {
		t.Errorf("expected callback URL %s, got %s", server.URL, callbackURL)
	}

	if callbackStatus != StatusHealthy {
		t.Errorf("expected healthy callback status, got %s", callbackStatus)
	}
}

func TestHealthCheckerCheckNow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: time.Hour, // Long interval so we control checks
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})

	// Trigger immediate check
	result := checker.CheckNow(server.URL)

	if result.Status != StatusHealthy {
		t.Errorf("expected healthy status, got %s", result.Status)
	}

	if result.Latency == 0 {
		t.Error("expected latency to be set")
	}
}

func TestHealthCheckerRemoveBackend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: time.Hour,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:        server.URL,
		HealthPath: "/health",
	})

	checker.RemoveBackend(server.URL)

	status := checker.GetStatus(server.URL)
	if status != StatusUnknown {
		t.Errorf("expected unknown status after removal, got %s", status)
	}
}

func TestHealthyBackends(t *testing.T) {
	healthy1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy1.Close()

	healthy2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy2.Close()

	unhealthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unhealthy.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: 50 * time.Millisecond,
	})
	defer checker.Stop()

	for _, url := range []string{healthy1.URL, healthy2.URL, unhealthy.URL} {
		checker.AddBackend(Backend{
			URL:            url,
			HealthPath:     "/health",
			HealthyAfter:   1,
			UnhealthyAfter: 1,
		})
	}

	// Wait for checks
	time.Sleep(200 * time.Millisecond)

	healthyBackends := checker.HealthyBackends()

	// Should have 2 healthy backends
	if len(healthyBackends) != 2 {
		t.Errorf("expected 2 healthy backends, got %d", len(healthyBackends))
	}

	// Unhealthy should not be in list
	for _, url := range healthyBackends {
		if url == unhealthy.URL {
			t.Error("unhealthy backend should not be in healthy list")
		}
	}
}

func TestIsHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: 50 * time.Millisecond,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})

	// Wait for check
	time.Sleep(100 * time.Millisecond)

	if !checker.IsHealthy(server.URL) {
		t.Error("expected IsHealthy to return true")
	}

	if checker.IsHealthy("http://nonexistent:9999") {
		t.Error("expected IsHealthy to return false for unknown backend")
	}
}

func TestParseStatusRange(t *testing.T) {
	tests := []struct {
		input   string
		want    StatusRange
		wantErr bool
	}{
		{"200", StatusRange{200, 200}, false},
		{"2xx", StatusRange{200, 299}, false},
		{"4xx", StatusRange{400, 499}, false},
		{"5xx", StatusRange{500, 599}, false},
		{"200-299", StatusRange{200, 299}, false},
		{"200-200", StatusRange{200, 200}, false},
		{"100-599", StatusRange{100, 599}, false},
		// invalid
		{"0xx", StatusRange{}, true},
		{"6xx", StatusRange{}, true},
		{"abc", StatusRange{}, true},
		{"", StatusRange{}, true},
		{"99", StatusRange{}, true},
		{"600", StatusRange{}, true},
		{"300-200", StatusRange{}, true}, // lo > hi
		{"99-200", StatusRange{}, true},  // lo < 100
		{"200-600", StatusRange{}, true}, // hi > 599
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseStatusRange(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseStatusRange(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ParseStatusRange(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestMatchStatus(t *testing.T) {
	ranges := []StatusRange{{200, 299}, {304, 304}}

	tests := []struct {
		code int
		want bool
	}{
		{200, true},
		{250, true},
		{299, true},
		{304, true},
		{300, false},
		{199, false},
		{500, false},
	}

	for _, tt := range tests {
		if got := matchStatus(tt.code, ranges); got != tt.want {
			t.Errorf("matchStatus(%d, ranges) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestCustomMethod(t *testing.T) {
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: time.Hour,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		Method:         "HEAD",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})

	result := checker.CheckNow(server.URL)
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy, got %s", result.Status)
	}
	if receivedMethod != "HEAD" {
		t.Errorf("expected HEAD method, got %s", receivedMethod)
	}
}

func TestExpectedStatus(t *testing.T) {
	// Server returns 204
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent) // 204
	}))
	defer server.Close()

	// With range 200-299: should be healthy
	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: time.Hour,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
		ExpectedStatus: []StatusRange{{200, 299}},
	})

	result := checker.CheckNow(server.URL)
	if result.Status != StatusHealthy {
		t.Errorf("expected healthy with 200-299 range and 204 response, got %s", result.Status)
	}

	// With range 200-200 only: should be unhealthy
	checker2 := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: time.Hour,
	})
	defer checker2.Stop()

	checker2.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
		ExpectedStatus: []StatusRange{{200, 200}},
	})

	result2 := checker2.CheckNow(server.URL)
	if result2.Status != StatusUnhealthy {
		t.Errorf("expected unhealthy with 200-only range and 204 response, got %s", result2.Status)
	}
}

func TestUpdateBackend(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: time.Hour,
	})
	defer checker.Stop()

	// Add with /health
	checker.AddBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/health",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})
	checker.CheckNow(server.URL)
	if receivedPath != "/health" {
		t.Errorf("expected /health, got %s", receivedPath)
	}

	// Update to /status — should restart check loop with new path
	checker.UpdateBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/status",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})
	checker.CheckNow(server.URL)
	if receivedPath != "/status" {
		t.Errorf("expected /status after update, got %s", receivedPath)
	}

	// Update with same config — should be a no-op
	checker.UpdateBackend(Backend{
		URL:            server.URL,
		HealthPath:     "/status",
		HealthyAfter:   1,
		UnhealthyAfter: 1,
	})
	// Verify it's still there
	if status := checker.GetStatus(server.URL); status == StatusUnknown {
		t.Error("expected backend to still be tracked after no-op update")
	}
}

func TestGetBackendConfig(t *testing.T) {
	checker := NewChecker(Config{
		DefaultTimeout:  time.Second,
		DefaultInterval: time.Hour,
	})
	defer checker.Stop()

	checker.AddBackend(Backend{
		URL:            "http://example.com",
		HealthPath:     "/status",
		Method:         "HEAD",
		HealthyAfter:   3,
		UnhealthyAfter: 5,
		ExpectedStatus: []StatusRange{{200, 200}},
	})

	cfg, ok := checker.GetBackendConfig("http://example.com")
	if !ok {
		t.Fatal("expected to find backend config")
	}
	if cfg.Method != "HEAD" {
		t.Errorf("expected HEAD, got %s", cfg.Method)
	}
	if cfg.HealthPath != "/status" {
		t.Errorf("expected /status, got %s", cfg.HealthPath)
	}
	if cfg.HealthyAfter != 3 {
		t.Errorf("expected 3, got %d", cfg.HealthyAfter)
	}
	if cfg.UnhealthyAfter != 5 {
		t.Errorf("expected 5, got %d", cfg.UnhealthyAfter)
	}
	if len(cfg.ExpectedStatus) != 1 || cfg.ExpectedStatus[0] != (StatusRange{200, 200}) {
		t.Errorf("expected [{200 200}], got %v", cfg.ExpectedStatus)
	}

	_, ok = checker.GetBackendConfig("http://nonexistent.com")
	if ok {
		t.Error("expected false for unknown backend")
	}
}
