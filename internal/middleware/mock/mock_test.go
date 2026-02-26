package mock

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/runway/config"
)

func TestMockHandler_DefaultStatus(t *testing.T) {
	h := New(config.MockResponseConfig{
		Enabled: true,
		Body:    `{"message":"mock"}`,
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != `{"message":"mock"}` {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
}

func TestMockHandler_CustomStatusAndHeaders(t *testing.T) {
	h := New(config.MockResponseConfig{
		Enabled:    true,
		StatusCode: 201,
		Headers: map[string]string{
			"Content-Type": "application/json",
			"X-Custom":     "test",
		},
		Body: `{"id":1}`,
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("expected 201, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("unexpected Content-Type: %s", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("X-Custom") != "test" {
		t.Errorf("unexpected X-Custom: %s", rec.Header().Get("X-Custom"))
	}
}

func TestMockHandler_EmptyBody(t *testing.T) {
	h := New(config.MockResponseConfig{
		Enabled:    true,
		StatusCode: 204,
	})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend should not be called")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Errorf("expected 204, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body, got %s", rec.Body.String())
	}
}

func TestMockHandler_ServedCounter(t *testing.T) {
	h := New(config.MockResponseConfig{Enabled: true})

	handler := h.Middleware()(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)
	handler.ServeHTTP(rec, req)

	if h.Served() != 2 {
		t.Errorf("expected 2 served, got %d", h.Served())
	}
}

func TestMockByRoute(t *testing.T) {
	m := NewMockByRoute()
	m.AddRoute("route1", config.MockResponseConfig{
		Enabled:    true,
		StatusCode: 200,
		Body:       "test",
	})

	if m.GetHandler("route1") == nil {
		t.Error("expected handler for route1")
	}
	if m.GetHandler("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
}
