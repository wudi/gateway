//go:build integration
// +build integration

package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestExtAuthIntegration_Allow(t *testing.T) {
	// Auth service that allows all requests and injects a header
	authService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Auth-User", "integration-user")
		w.WriteHeader(http.StatusOK)
	}))
	defer authService.Close()

	// Backend that returns the received headers
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"x_auth_user": r.Header.Get("X-Auth-User"),
			"path":        r.URL.Path,
		})
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:         "ext-auth-allow",
			Path:       "/api",
			PathPrefix: true,
			Backends:   []config.BackendConfig{{URL: backend.URL}},
			ExtAuth: config.ExtAuthConfig{
				Enabled:         true,
				URL:             authService.URL,
				HeadersToInject: []string{"X-Auth-User"},
			},
		},
	}

	_, ts := newTestRunway(t, cfg)

	resp, err := http.Get(ts.URL + "/api/users")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)

	if body["x_auth_user"] != "integration-user" {
		t.Errorf("expected X-Auth-User=integration-user in backend, got %q", body["x_auth_user"])
	}
}

func TestExtAuthIntegration_Deny(t *testing.T) {
	// Auth service that denies all requests
	authService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"not authorized"}`))
	}))
	defer authService.Close()

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		t.Error("backend should not be reached on deny")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:         "ext-auth-deny",
			Path:       "/api",
			PathPrefix: true,
			Backends:   []config.BackendConfig{{URL: backend.URL}},
			ExtAuth: config.ExtAuthConfig{
				Enabled: true,
				URL:     authService.URL,
			},
		},
	}

	_, ts := newTestRunway(t, cfg)

	resp, err := http.Get(ts.URL + "/api/secret")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"error":"not authorized"}` {
		t.Errorf("unexpected body: %s", body)
	}
}

func TestExtAuthIntegration_FailOpen(t *testing.T) {
	// Auth service that is down
	authService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	authService.Close() // close immediately to simulate unreachable

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "reached"})
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:         "ext-auth-failopen",
			Path:       "/api",
			PathPrefix: true,
			Backends:   []config.BackendConfig{{URL: backend.URL}},
			ExtAuth: config.ExtAuthConfig{
				Enabled:  true,
				URL:      authService.URL,
				FailOpen: true,
			},
		},
	}

	_, ts := newTestRunway(t, cfg)

	resp, err := http.Get(ts.URL + "/api/data")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (fail_open), got %d", resp.StatusCode)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "reached" {
		t.Error("expected backend to be reached with fail_open")
	}
}
