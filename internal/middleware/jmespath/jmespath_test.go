package jmespath

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestJMESPath_BasicExpression(t *testing.T) {
	cfg := config.JMESPathConfig{
		Enabled:    true,
		Expression: "people[?age > `20`].name",
	}

	jp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"people": []interface{}{
				map[string]interface{}{"name": "Alice", "age": 25},
				map[string]interface{}{"name": "Bob", "age": 18},
				map[string]interface{}{"name": "Charlie", "age": 30},
			},
		})
	})

	handler := jp.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}

	var result []interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}

	names := make([]string, len(result))
	for i, v := range result {
		names[i] = v.(string)
	}
	if names[0] != "Alice" || names[1] != "Charlie" {
		t.Fatalf("unexpected names: %v", names)
	}

	if jp.Applied() != 1 {
		t.Fatalf("expected 1 applied, got %d", jp.Applied())
	}
}

func TestJMESPath_WrapCollections(t *testing.T) {
	cfg := config.JMESPathConfig{
		Enabled:         true,
		Expression:      "items[].name",
		WrapCollections: true,
	}

	jp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"name": "one", "value": 1},
				map[string]interface{}{"name": "two", "value": 2},
			},
		})
	})

	handler := jp.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	collection, ok := result["collection"]
	if !ok {
		t.Fatal("expected 'collection' key in wrapped result")
	}

	items, ok := collection.([]interface{})
	if !ok {
		t.Fatal("expected collection to be an array")
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0] != "one" || items[1] != "two" {
		t.Fatalf("unexpected items: %v", items)
	}
}

func TestJMESPath_WrapCollections_NonArray(t *testing.T) {
	cfg := config.JMESPathConfig{
		Enabled:         true,
		Expression:      "items[0].name",
		WrapCollections: true,
	}

	jp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []interface{}{
				map[string]interface{}{"name": "one"},
			},
		})
	})

	handler := jp.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Non-array result should NOT be wrapped even when wrapCollections is true
	var result string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("expected scalar string result, got: %s", w.Body.String())
	}
	if result != "one" {
		t.Fatalf("expected 'one', got %q", result)
	}
}

func TestJMESPath_NonJSONPassthrough(t *testing.T) {
	cfg := config.JMESPathConfig{
		Enabled:    true,
		Expression: "name",
	}

	jp, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello world"))
	})

	handler := jp.Middleware()(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Body.String() != "hello world" {
		t.Fatalf("expected passthrough of non-JSON body, got %q", w.Body.String())
	}

	if jp.Applied() != 0 {
		t.Fatalf("expected 0 applied for non-JSON, got %d", jp.Applied())
	}
}

func TestJMESPath_InvalidExpression(t *testing.T) {
	cfg := config.JMESPathConfig{
		Enabled:    true,
		Expression: "[[[invalid",
	}

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for invalid expression")
	}
}

func TestJMESPath_EmptyExpression(t *testing.T) {
	cfg := config.JMESPathConfig{
		Enabled:    true,
		Expression: "",
	}

	_, err := New(cfg)
	if err == nil {
		t.Fatal("expected error for empty expression")
	}
}

func TestJMESPathByRoute(t *testing.T) {
	mgr := NewJMESPathByRoute()

	err := mgr.AddRoute("route1", config.JMESPathConfig{
		Enabled:    true,
		Expression: "name",
	})
	if err != nil {
		t.Fatal(err)
	}

	jp := mgr.GetJMESPath("route1")
	if jp == nil {
		t.Fatal("expected non-nil JMESPath for route1")
	}

	missing := mgr.GetJMESPath("nonexistent")
	if missing != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	stats := mgr.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Fatal("expected stats for route1")
	}
}
