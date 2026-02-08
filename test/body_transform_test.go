// +build integration

package test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/gateway"
)

func TestBodyTransformIntegration_SetAndDenyFields(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo request body fields in response, plus add backend-only fields
		body, _ := io.ReadAll(r.Body)
		var reqData map[string]interface{}
		if len(body) > 0 {
			json.Unmarshal(body, &reqData)
		}

		resp := map[string]interface{}{
			"name":          "alice",
			"password_hash": "secret123",
			"internal_id":   "xyz",
			"result":        "ok",
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:         "transform-test",
				Path:       "/api",
				PathPrefix: true,
				Backends: []config.BackendConfig{
					{URL: backend.URL},
				},
				Transform: config.TransformConfig{
					Request: config.RequestTransform{
						Body: config.BodyTransformConfig{
							SetFields: map[string]string{
								"metadata.source": "gateway",
							},
							RemoveFields: []string{"debug"},
						},
					},
					Response: config.ResponseTransform{
						Body: config.BodyTransformConfig{
							DenyFields: []string{"password_hash", "internal_id"},
							SetFields: map[string]string{
								"meta.served_by": "gateway",
							},
						},
					},
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

	// Send request with a debug field that should be stripped
	reqBody := `{"user":"test","debug":true}`
	resp, err := http.Post(ts.URL+"/api/data", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Response should not contain denied fields
	if _, ok := result["password_hash"]; ok {
		t.Error("expected password_hash to be denied from response")
	}
	if _, ok := result["internal_id"]; ok {
		t.Error("expected internal_id to be denied from response")
	}

	// Response should contain the set_fields
	meta, ok := result["meta"].(map[string]interface{})
	if !ok {
		t.Fatal("expected meta to be present in response")
	}
	if meta["served_by"] != "gateway" {
		t.Errorf("expected meta.served_by=gateway, got %v", meta["served_by"])
	}

	// Original fields should still be there
	if result["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", result["name"])
	}
	if result["result"] != "ok" {
		t.Errorf("expected result=ok, got %v", result["result"])
	}
}

func TestBodyTransformIntegration_Template(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"users": []string{"alice", "bob"},
		})
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:         "template-test",
				Path:       "/template",
				PathPrefix: true,
				Backends: []config.BackendConfig{
					{URL: backend.URL},
				},
				Transform: config.TransformConfig{
					Response: config.ResponseTransform{
						Body: config.BodyTransformConfig{
							Template: `{"data": {{.body | json}}, "wrapped": true}`,
						},
					},
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

	resp, err := http.Get(ts.URL + "/template/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result["wrapped"] != true {
		t.Error("expected wrapped=true")
	}
	data, ok := result["data"].(map[string]interface{})
	if !ok {
		t.Fatal("expected data to be an object")
	}
	users, ok := data["users"].([]interface{})
	if !ok {
		t.Fatal("expected data.users to be an array")
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

func TestBodyTransformIntegration_AllowFields(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"name":   "alice",
			"email":  "a@b.com",
			"secret": "xyz",
			"admin":  true,
		})
	}))
	defer backend.Close()

	cfg := &config.Config{
		Registry: config.RegistryConfig{
			Type: "memory",
		},
		Routes: []config.RouteConfig{
			{
				ID:         "allow-test",
				Path:       "/allow",
				PathPrefix: true,
				Backends: []config.BackendConfig{
					{URL: backend.URL},
				},
				Transform: config.TransformConfig{
					Response: config.ResponseTransform{
						Body: config.BodyTransformConfig{
							AllowFields: []string{"name", "email"},
						},
					},
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

	resp, err := http.Get(ts.URL + "/allow/test")
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if result["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", result["name"])
	}
	if result["email"] != "a@b.com" {
		t.Errorf("expected email=a@b.com, got %v", result["email"])
	}
	if _, ok := result["secret"]; ok {
		t.Error("expected secret to be filtered out by allow_fields")
	}
	if _, ok := result["admin"]; ok {
		t.Error("expected admin to be filtered out by allow_fields")
	}
}
