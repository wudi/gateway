package errorhandling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/variables"
)

func TestDefaultMode_Passthrough(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "default"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 500 {
		t.Errorf("expected status 500, got %d", w.Code)
	}
	if w.Body.String() != "internal error" {
		t.Errorf("expected original body, got %q", w.Body.String())
	}
}

func TestDefaultMode_EmptyConfig(t *testing.T) {
	h := New(config.ErrorHandlingConfig{})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Errorf("expected status 404, got %d", w.Code)
	}
	if w.Body.String() != "not found" {
		t.Errorf("expected original body, got %q", w.Body.String())
	}
}

func TestPassStatusMode_ErrorStatus(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "pass_status"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		w.Write([]byte("bad runway from backend"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 502 {
		t.Errorf("expected status 502, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON body: %v", err)
	}
	if body["error"] != "runway error" {
		t.Errorf("expected error='runway error', got %v", body["error"])
	}
	if body["status"].(float64) != 502 {
		t.Errorf("expected status=502, got %v", body["status"])
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", w.Header().Get("Content-Type"))
	}
}

func TestPassStatusMode_SuccessPassthrough(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "pass_status"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", w.Body.String())
	}
}

func TestDetailedMode_ErrorStatus(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "detailed"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("resource not found"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)

	// Set up variable context with a route ID.
	vc := &variables.Context{
		RouteID: "my-route",
		Request: r,
	}
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, vc)
	r = r.WithContext(ctx)

	handler.ServeHTTP(w, r)

	if w.Code != 404 {
		t.Errorf("expected status 404, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON body: %v", err)
	}

	errObj, ok := body["error_my-route"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error_my-route key, got keys: %v", body)
	}
	if errObj["status"].(float64) != 404 {
		t.Errorf("expected status=404, got %v", errObj["status"])
	}
	if errObj["body"] != "resource not found" {
		t.Errorf("expected body='resource not found', got %v", errObj["body"])
	}
}

func TestDetailedMode_NoRouteID(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "detailed"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("fail"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON body: %v", err)
	}
	if _, ok := body["error_unknown"]; !ok {
		t.Errorf("expected error_unknown key when no route ID, got keys: %v", body)
	}
}

func TestDetailedMode_SuccessPassthrough(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "detailed"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte("created"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 201 {
		t.Errorf("expected status 201, got %d", w.Code)
	}
	if w.Body.String() != "created" {
		t.Errorf("expected body 'created', got %q", w.Body.String())
	}
}

func TestMessageMode_ErrorStatus(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "message"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		w.Write([]byte("service unavailable"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected status 200 for message mode, got %d", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse JSON body: %v", err)
	}
	if body["message"] != "backend returned error" {
		t.Errorf("expected message='backend returned error', got %v", body["message"])
	}
	if body["status"].(float64) != 503 {
		t.Errorf("expected status=503, got %v", body["status"])
	}
}

func TestMessageMode_SuccessPassthrough(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "message"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("all good"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if w.Body.String() != "all good" {
		t.Errorf("expected body 'all good', got %q", w.Body.String())
	}
}

func TestCounters(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "pass_status"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("err"))
	}))

	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		handler.ServeHTTP(w, r)
	}

	// Also send a success request.
	okHandler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	okHandler.ServeHTTP(w, r)

	stats := h.Stats()
	if stats["total"].(int64) != 4 {
		t.Errorf("expected total=4, got %v", stats["total"])
	}
	if stats["reformatted"].(int64) != 3 {
		t.Errorf("expected reformatted=3, got %v", stats["reformatted"])
	}
	if stats["mode"].(string) != "pass_status" {
		t.Errorf("expected mode=pass_status, got %v", stats["mode"])
	}
}

func TestErrorHandlerByRoute(t *testing.T) {
	m := NewErrorHandlerByRoute()
	m.AddRoute("r1", config.ErrorHandlingConfig{Mode: "pass_status"})
	m.AddRoute("r2", config.ErrorHandlingConfig{Mode: "detailed"})

	if h := m.Lookup("r1"); h == nil {
		t.Fatal("expected handler for r1")
	} else if h.mode != "pass_status" {
		t.Errorf("expected mode pass_status for r1, got %s", h.mode)
	}

	if h := m.Lookup("r2"); h == nil {
		t.Fatal("expected handler for r2")
	} else if h.mode != "detailed" {
		t.Errorf("expected mode detailed for r2, got %s", h.mode)
	}

	if h := m.Lookup("nonexistent"); h != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := m.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
}

func TestImplicitOK(t *testing.T) {
	h := New(config.ErrorHandlingConfig{Mode: "pass_status"})
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No explicit WriteHeader — implicit 200.
		w.Write([]byte("implicit ok"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected status 200 for implicit OK, got %d", w.Code)
	}
	if w.Body.String() != "implicit ok" {
		t.Errorf("expected body 'implicit ok', got %q", w.Body.String())
	}
}
