package aggregate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
)

func TestAggregateHandler_BasicMerge(t *testing.T) {
	// Backend 1 returns {"name": "alice"}
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"name": "alice"})
	}))
	defer s1.Close()

	// Backend 2 returns {"age": 30}
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"age": 30})
	}))
	defer s2.Close()

	cfg := config.AggregateConfig{
		Enabled: true,
		Timeout: 5 * time.Second,
		Backends: []config.AggregateBackend{
			{Name: "users", URL: s1.URL},
			{Name: "ages", URL: s2.URL},
		},
	}

	ah, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	ah.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	if result["name"] != "alice" {
		t.Errorf("expected name=alice, got %v", result["name"])
	}
	if result["age"] != float64(30) {
		t.Errorf("expected age=30, got %v", result["age"])
	}
}

func TestAggregateHandler_GroupedMerge(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": "alice"})
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"total": 42})
	}))
	defer s2.Close()

	cfg := config.AggregateConfig{
		Enabled: true,
		Backends: []config.AggregateBackend{
			{Name: "user", URL: s1.URL, Group: "user"},
			{Name: "stats", URL: s2.URL, Group: "stats"},
		},
	}

	ah, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	ah.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)

	userMap, ok := result["user"].(map[string]interface{})
	if !ok {
		t.Fatal("expected user group")
	}
	if userMap["name"] != "alice" {
		t.Errorf("expected user.name=alice, got %v", userMap["name"])
	}

	statsMap, ok := result["stats"].(map[string]interface{})
	if !ok {
		t.Fatal("expected stats group")
	}
	if statsMap["total"] != float64(42) {
		t.Errorf("expected stats.total=42, got %v", statsMap["total"])
	}
}

func TestAggregateHandler_AbortOnFailure(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer s2.Close()

	cfg := config.AggregateConfig{
		Enabled:      true,
		FailStrategy: "abort",
		Backends: []config.AggregateBackend{
			{Name: "ok", URL: s1.URL},
			{Name: "fail", URL: s2.URL},
		},
	}

	ah, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	ah.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", w.Code)
	}
}

func TestAggregateHandler_PartialMode(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer s2.Close()

	cfg := config.AggregateConfig{
		Enabled:      true,
		FailStrategy: "partial",
		Backends: []config.AggregateBackend{
			{Name: "ok", URL: s1.URL},
			{Name: "fail", URL: s2.URL},
		},
	}

	ah, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	ah.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	if w.Header().Get("X-Aggregate-Partial") != "true" {
		t.Error("expected X-Aggregate-Partial header")
	}

	var result map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &result)

	if result["_errors"] == nil {
		t.Error("expected _errors array in partial response")
	}
}

func TestAggregateHandler_RequiredBackendInPartial(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer s2.Close()

	cfg := config.AggregateConfig{
		Enabled:      true,
		FailStrategy: "partial",
		Backends: []config.AggregateBackend{
			{Name: "ok", URL: s1.URL},
			{Name: "fail", URL: s2.URL, Required: true},
		},
	}

	ah, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	ah.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected 502 for required failure, got %d", w.Code)
	}
}

func TestAggregateHandler_URLTemplate(t *testing.T) {
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path})
	}))
	defer s1.Close()

	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"host": r.Host})
	}))
	defer s2.Close()

	cfg := config.AggregateConfig{
		Enabled: true,
		Backends: []config.AggregateBackend{
			{Name: "b1", URL: s1.URL + "/api{{.Path}}", Group: "b1"},
			{Name: "b2", URL: s2.URL + "/info", Group: "b2"},
		},
	}

	ah, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	ah.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNew_ValidationErrors(t *testing.T) {
	// Too few backends
	_, err := New(config.AggregateConfig{
		Backends: []config.AggregateBackend{{Name: "one", URL: "http://localhost"}},
	}, http.DefaultTransport)
	if err == nil {
		t.Error("expected error for < 2 backends")
	}

	// Invalid URL template
	_, err = New(config.AggregateConfig{
		Backends: []config.AggregateBackend{
			{Name: "a", URL: "{{.Invalid"},
			{Name: "b", URL: "http://localhost"},
		},
	}, http.DefaultTransport)
	if err == nil {
		t.Error("expected error for invalid URL template")
	}
}

func TestAggregateByRoute(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer s.Close()

	m := NewAggregateByRoute()
	cfg := config.AggregateConfig{
		Enabled: true,
		Backends: []config.AggregateBackend{
			{Name: "a", URL: s.URL},
			{Name: "b", URL: s.URL},
		},
	}

	if err := m.AddRoute("r1", cfg, http.DefaultTransport); err != nil {
		t.Fatal(err)
	}

	if m.GetHandler("r1") == nil {
		t.Error("expected handler for r1")
	}
	if m.GetHandler("r2") != nil {
		t.Error("expected nil for r2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "r1" {
		t.Errorf("expected [r1], got %v", ids)
	}

	stats := m.Stats()
	if stats["r1"] == nil {
		t.Error("expected stats for r1")
	}
}

func TestAggregateHandler_Stats(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer s.Close()

	cfg := config.AggregateConfig{
		Enabled: true,
		Backends: []config.AggregateBackend{
			{Name: "a", URL: s.URL},
			{Name: "b", URL: s.URL},
		},
	}

	ah, err := New(cfg, http.DefaultTransport)
	if err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	ah.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	stats := ah.Stats()
	if stats["total_requests"] != int64(1) {
		t.Errorf("expected 1 total request, got %v", stats["total_requests"])
	}
	if stats["total_errors"] != int64(0) {
		t.Errorf("expected 0 errors, got %v", stats["total_errors"])
	}
}
