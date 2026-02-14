package sequential

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestSequentialHandler_BasicChain(t *testing.T) {
	// Step 0: GET /users/1 → {"id":1,"name":"alice"}
	// Step 1: GET /posts?author=alice → {"posts":["p1","p2"]}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/users/1":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"id": 1, "name": "alice"})
		case "/posts":
			author := r.URL.Query().Get("author")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"posts": []string{"p1", "p2"}, "author": author})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := config.SequentialConfig{
		Enabled: true,
		Steps: []config.SequentialStep{
			{
				URL:     server.URL + "/users/1",
				Method:  "GET",
				Timeout: 5 * time.Second,
			},
			{
				URL:     server.URL + `/posts?author={{index .Responses "Resp0" "name"}}`,
				Method:  "GET",
				Timeout: 5 * time.Second,
			},
		},
	}

	sh, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/api/user-posts", nil)
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp["author"] != "alice" {
		t.Errorf("expected author=alice, got %v", resp["author"])
	}

	stats := sh.Stats()
	if stats["total_requests"].(int64) != 1 {
		t.Errorf("expected 1 total request, got %v", stats["total_requests"])
	}
	if stats["total_errors"].(int64) != 0 {
		t.Errorf("expected 0 errors, got %v", stats["total_errors"])
	}
}

func TestSequentialHandler_WithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{"token": "abc123"})
		case "/data":
			body, _ := io.ReadAll(r.Body)
			authHeader := r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"auth":    authHeader,
				"request": string(body),
			})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	cfg := config.SequentialConfig{
		Enabled: true,
		Steps: []config.SequentialStep{
			{
				URL:     server.URL + "/auth",
				Method:  "POST",
				Timeout: 5 * time.Second,
			},
			{
				URL:    server.URL + "/data",
				Method: "POST",
				Headers: map[string]string{
					"Authorization": `Bearer {{index .Responses "Resp0" "token"}}`,
				},
				BodyTemplate: `{"token":"{{index .Responses "Resp0" "token"}}"}`,
				Timeout:      5 * time.Second,
			},
		},
	}

	sh, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/flow", nil)
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["auth"] != "Bearer abc123" {
		t.Errorf("expected Bearer abc123, got %v", resp["auth"])
	}
}

func TestSequentialHandler_StepFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"fail"}`))
	}))
	defer server.Close()

	cfg := config.SequentialConfig{
		Enabled: true,
		Steps: []config.SequentialStep{
			{
				URL:     server.URL + "/step1",
				Timeout: 5 * time.Second,
			},
			{
				URL:     server.URL + "/step2",
				Timeout: 5 * time.Second,
			},
		},
	}

	sh, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	// Last step's status code is returned
	if w.Code != 500 {
		t.Errorf("expected 500, got %d", w.Code)
	}
}

func TestSequentialHandler_InvalidURLTemplate(t *testing.T) {
	cfg := config.SequentialConfig{
		Enabled: true,
		Steps: []config.SequentialStep{
			{
				URL: "{{.Invalid",
			},
		},
	}
	_, err := New(cfg, http.DefaultTransport)
	if err == nil {
		t.Fatal("expected error for invalid URL template")
	}
}

func TestSequentialHandler_RequestContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	cfg := config.SequentialConfig{
		Enabled: true,
		Steps: []config.SequentialStep{
			{
				URL:     server.URL + "/api",
				Timeout: 5 * time.Second,
			},
			{
				URL:     server.URL + "/end",
				Timeout: 5 * time.Second,
			},
		},
	}

	sh, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", "/trigger?search=hello", nil)
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestSequentialByRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
	}))
	defer server.Close()

	m := NewSequentialByRoute()

	cfg := config.SequentialConfig{
		Enabled: true,
		Steps: []config.SequentialStep{
			{URL: server.URL + "/a", Timeout: time.Second},
			{URL: server.URL + "/b", Timeout: time.Second},
		},
	}

	if err := m.AddRoute("route1", cfg, http.DefaultTransport); err != nil {
		t.Fatal(err)
	}

	if m.GetHandler("route1") == nil {
		t.Error("expected handler for route1")
	}
	if m.GetHandler("route2") != nil {
		t.Error("expected nil for route2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 {
		t.Errorf("expected 1 route, got %d", len(ids))
	}

	stats := m.Stats()
	if len(stats) != 1 {
		t.Errorf("expected 1 route in stats, got %d", len(stats))
	}
}
