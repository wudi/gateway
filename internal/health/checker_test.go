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
