//go:build integration
// +build integration

package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestStickyCookieSessionIntegration(t *testing.T) {
	stableBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"group": "stable"})
	}))
	defer stableBackend.Close()

	canaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"group": "canary"})
	}))
	defer canaryBackend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "sticky-test",
			Path: "/sticky",
			Backends: []config.BackendConfig{
				{URL: stableBackend.URL},
			},
			TrafficSplit: []config.TrafficSplitConfig{
				{
					Name:     "stable",
					Weight:   90,
					Backends: []config.BackendConfig{{URL: stableBackend.URL}},
				},
				{
					Name:     "canary",
					Weight:   10,
					Backends: []config.BackendConfig{{URL: canaryBackend.URL}},
				},
			},
			Sticky: config.StickyConfig{
				Enabled:    true,
				Mode:       "cookie",
				CookieName: "X-Traffic-Group",
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// First request without cookie — gets assigned to some group
	resp, err := http.Get(ts.URL + "/sticky")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Should get X-AB-Variant header
	variant := resp.Header.Get("X-AB-Variant")
	if variant == "" {
		t.Fatal("expected X-AB-Variant header")
	}
	if variant != "stable" && variant != "canary" {
		t.Fatalf("unexpected variant: %s", variant)
	}

	// Should get a sticky cookie
	var stickyCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "X-Traffic-Group" {
			stickyCookie = c
			break
		}
	}
	if stickyCookie == nil {
		t.Fatal("expected X-Traffic-Group cookie")
	}
	if stickyCookie.Value != variant {
		t.Errorf("cookie value %q doesn't match variant %q", stickyCookie.Value, variant)
	}

	// Subsequent requests with cookie should always go to the same group
	for i := 0; i < 20; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/sticky", nil)
		req.AddCookie(stickyCookie)

		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()

		var result map[string]string
		json.Unmarshal(b, &result)

		if result["group"] != variant {
			t.Errorf("request %d: expected group %s, got %s", i, variant, result["group"])
		}

		v := r.Header.Get("X-AB-Variant")
		if v != variant {
			t.Errorf("request %d: expected X-AB-Variant %s, got %s", i, variant, v)
		}
	}
}

func TestStickyHashSessionIntegration(t *testing.T) {
	stableBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"group": "stable"})
	}))
	defer stableBackend.Close()

	canaryBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"group": "canary"})
	}))
	defer canaryBackend.Close()

	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "hash-sticky",
			Path: "/hash",
			Backends: []config.BackendConfig{
				{URL: stableBackend.URL},
			},
			TrafficSplit: []config.TrafficSplitConfig{
				{
					Name:     "stable",
					Weight:   50,
					Backends: []config.BackendConfig{{URL: stableBackend.URL}},
				},
				{
					Name:     "canary",
					Weight:   50,
					Backends: []config.BackendConfig{{URL: canaryBackend.URL}},
				},
			},
			Sticky: config.StickyConfig{
				Enabled: true,
				Mode:    "hash",
				HashKey: "X-User-ID",
			},
		},
	}

	_, ts := newTestGateway(t, cfg)

	// Same user should always get the same group
	var firstGroup string
	for i := 0; i < 20; i++ {
		req, _ := http.NewRequest("GET", ts.URL+"/hash", nil)
		req.Header.Set("X-User-ID", "user-42")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var result map[string]string
		json.Unmarshal(body, &result)

		if firstGroup == "" {
			firstGroup = result["group"]
		} else if result["group"] != firstGroup {
			t.Fatalf("hash sticky not deterministic: got %s, want %s", result["group"], firstGroup)
		}
	}
}

func TestStickyValidation(t *testing.T) {
	loader := config.NewLoader()

	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "sticky without traffic_split",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
    sticky:
      enabled: true
      mode: cookie
`,
			wantErr: "sticky requires traffic_split",
		},
		{
			name: "sticky without mode",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
    traffic_split:
      - name: a
        weight: 100
        backends:
          - url: http://localhost:9999
    sticky:
      enabled: true
`,
			wantErr: "sticky.mode is required",
		},
		{
			name: "hash mode without hash_key",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
    traffic_split:
      - name: a
        weight: 100
        backends:
          - url: http://localhost:9999
    sticky:
      enabled: true
      mode: hash
`,
			wantErr: "sticky.hash_key is required",
		},
		{
			name: "traffic_split weights not 100",
			yaml: `
listeners:
  - id: default
    address: ":8080"
    protocol: http
routes:
  - id: r1
    path: /test
    backends:
      - url: http://localhost:9999
    traffic_split:
      - name: a
        weight: 60
        backends:
          - url: http://localhost:9999
      - name: b
        weight: 30
        backends:
          - url: http://localhost:9998
`,
			wantErr: "traffic_split weights must sum to 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loader.Parse([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

func TestTrafficSplitAdminEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			return
		}
		w.WriteHeader(200)
	}))
	defer backend.Close()

	cfg := baseConfig()
	cfg.Admin = config.AdminConfig{Enabled: true, Port: 0}
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "split-route",
			Path: "/split",
			Backends: []config.BackendConfig{
				{URL: backend.URL},
			},
			TrafficSplit: []config.TrafficSplitConfig{
				{
					Name:     "stable",
					Weight:   80,
					Backends: []config.BackendConfig{{URL: backend.URL}},
				},
				{
					Name:     "canary",
					Weight:   20,
					Backends: []config.BackendConfig{{URL: backend.URL}},
				},
			},
			Sticky: config.StickyConfig{
				Enabled:    true,
				Mode:       "cookie",
				CookieName: "X-Traffic-Group",
			},
		},
	}

	gw, _ := newTestGateway(t, cfg)

	stats := gw.GetTrafficSplitStats()
	info, ok := stats["split-route"]
	if !ok {
		t.Fatal("expected traffic split stats for split-route")
	}

	infoMap := info.(map[string]interface{})
	if sticky, ok := infoMap["sticky"].(bool); !ok || !sticky {
		t.Error("expected sticky=true in stats")
	}

	groups := infoMap["groups"].([]map[string]interface{})
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
}
