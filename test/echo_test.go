//go:build integration
// +build integration

package test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/wudi/runway/config"
)

func TestEchoRouteIntegration(t *testing.T) {
	cfg := baseConfig()
	cfg.Routes = []config.RouteConfig{
		{
			ID:   "echo",
			Path: "/echo",
			Echo: true,
		},
	}

	_, ts := newTestRunway(t, cfg)

	t.Run("GET echo", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/echo?key=value")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("content-type: got %q, want application/json", ct)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if body["method"] != "GET" {
			t.Errorf("method: got %v, want GET", body["method"])
		}
		if body["path"] != "/echo" {
			t.Errorf("path: got %v, want /echo", body["path"])
		}
		if body["route_id"] != "echo" {
			t.Errorf("route_id: got %v, want echo", body["route_id"])
		}

		query, ok := body["query"].(map[string]interface{})
		if !ok {
			t.Fatal("query not a map")
		}
		if query["key"] != "value" {
			t.Errorf("query key: got %v, want value", query["key"])
		}
	})

	t.Run("POST echo with body", func(t *testing.T) {
		resp, err := http.Post(ts.URL+"/echo", "text/plain", strings.NewReader("hello world"))
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()

		var body map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}

		if body["method"] != "POST" {
			t.Errorf("method: got %v, want POST", body["method"])
		}
		if body["body"] != "hello world" {
			t.Errorf("body: got %v, want hello world", body["body"])
		}
	})
}
