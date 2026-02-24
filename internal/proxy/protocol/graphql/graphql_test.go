package graphql

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestGraphQLHandler(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		if req["query"] == nil {
			t.Error("expected query field")
		}

		resp := map[string]interface{}{
			"data": map[string]interface{}{
				"user": map[string]interface{}{
					"name":  "Alice",
					"email": "alice@example.com",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer backend.Close()

	cfg := config.GraphQLProtocolConfig{
		URL:           backend.URL,
		Type:          "query",
		Query:         `query GetUser($id: ID!) { user(id: $id) { name email } }`,
		OperationName: "GetUser",
		Variables: map[string]string{
			"id": "123",
		},
	}

	h, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/users/123", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)
	user, ok := result["user"].(map[string]interface{})
	if !ok {
		t.Fatal("expected user object in data extraction")
	}
	if user["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", user["name"])
	}
}

func TestGraphQLHandlerValidation(t *testing.T) {
	_, err := New(config.GraphQLProtocolConfig{}, http.DefaultTransport)
	if err == nil {
		t.Error("expected error for empty config")
	}

	_, err = New(config.GraphQLProtocolConfig{URL: "http://example.com"}, http.DefaultTransport)
	if err == nil {
		t.Error("expected error for missing query")
	}
}

func TestGraphQLByRoute(t *testing.T) {
	m := NewGraphQLByRoute()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"ok": true}})
	}))
	defer backend.Close()

	cfg := config.GraphQLProtocolConfig{
		URL:   backend.URL,
		Query: `{ status }`,
	}
	if err := m.AddRoute("route1", cfg, http.DefaultTransport); err != nil {
		t.Fatal(err)
	}

	h := m.GetHandler("route1")
	if h == nil {
		t.Fatal("expected handler")
	}

	stats := m.Stats()
	if stats["route1"] == nil {
		t.Error("expected route1 stats")
	}
}
