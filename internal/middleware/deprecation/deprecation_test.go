package deprecation

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestDeprecation_HeadersInjected(t *testing.T) {
	sunset := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	h, err := New(config.DeprecationConfig{
		Enabled:    true,
		SunsetDate: sunset,
		Message:    "Use v2 instead",
		Link:       "https://api.example.com/v2",
	})
	if err != nil {
		t.Fatal(err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	handler := h.Middleware()(backend)

	req := httptest.NewRequest("GET", "/api/v1/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Deprecation") != "true" {
		t.Fatal("expected Deprecation: true header")
	}
	if rec.Header().Get("Sunset") == "" {
		t.Fatal("expected Sunset header")
	}
	if rec.Header().Get("Link") == "" {
		t.Fatal("expected Link header")
	}
}

func TestDeprecation_NoSunsetDate(t *testing.T) {
	h, err := New(config.DeprecationConfig{
		Enabled: true,
		Message: "This API is deprecated",
	})
	if err != nil {
		t.Fatal(err)
	}

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	handler := h.Middleware()(backend)

	req := httptest.NewRequest("GET", "/api/old", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if rec.Header().Get("Deprecation") != "true" {
		t.Fatal("expected Deprecation: true header")
	}
	if rec.Header().Get("Sunset") != "" {
		t.Fatal("expected no Sunset header without sunset_date")
	}
}

func TestDeprecation_BlockAfterSunset(t *testing.T) {
	past := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	h, err := New(config.DeprecationConfig{
		Enabled:    true,
		SunsetDate: past,
		ResponseAfterSunset: &config.SunsetResponse{
			Status: 410,
			Body:   `{"error":"API has been sunset"}`,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	backendCalled := false
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(200)
	})

	handler := h.Middleware()(backend)

	req := httptest.NewRequest("GET", "/api/v1/sunset", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if backendCalled {
		t.Fatal("backend should not be called after sunset")
	}
	if rec.Code != 410 {
		t.Fatalf("expected 410, got %d", rec.Code)
	}
	if rec.Header().Get("Deprecation") != "true" {
		t.Fatal("expected Deprecation header even on blocked response")
	}
	if rec.Body.String() != `{"error":"API has been sunset"}` {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestDeprecation_DefaultStatus410(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	h, err := New(config.DeprecationConfig{
		Enabled:    true,
		SunsetDate: past,
		ResponseAfterSunset: &config.SunsetResponse{
			Body: "gone",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not reach backend")
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 410 {
		t.Fatalf("expected default 410, got %d", rec.Code)
	}
}

func TestDeprecation_FutureSunsetNoBlock(t *testing.T) {
	future := time.Now().Add(720 * time.Hour).Format(time.RFC3339)
	h, err := New(config.DeprecationConfig{
		Enabled:    true,
		SunsetDate: future,
		ResponseAfterSunset: &config.SunsetResponse{
			Status: 410,
			Body:   "gone",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	backendCalled := false
	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled = true
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !backendCalled {
		t.Fatal("backend should be called before sunset")
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestDeprecation_CustomLinkRelation(t *testing.T) {
	h, err := New(config.DeprecationConfig{
		Enabled:      true,
		Link:         "https://api.example.com/v3",
		LinkRelation: "alternate",
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	expected := `<https://api.example.com/v3>; rel="alternate"`
	if rec.Header().Get("Link") != expected {
		t.Fatalf("expected Link %q, got %q", expected, rec.Header().Get("Link"))
	}
}

func TestDeprecation_InvalidSunsetDate(t *testing.T) {
	_, err := New(config.DeprecationConfig{
		Enabled:    true,
		SunsetDate: "not-a-date",
	})
	if err == nil {
		t.Fatal("expected error for invalid sunset_date")
	}
}

func TestDeprecation_Stats(t *testing.T) {
	h, err := New(config.DeprecationConfig{
		Enabled: true,
		Message: "deprecated",
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	stats := h.Stats()
	if stats["requests_total"].(int64) != 3 {
		t.Fatalf("expected 3 requests, got %v", stats["requests_total"])
	}
}

func TestDeprecationByRoute(t *testing.T) {
	m := NewDeprecationByRoute()
	err := m.AddRoute("route1", config.DeprecationConfig{
		Enabled: true,
		Message: "deprecated",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := m.Lookup("route1")
	if h == nil {
		t.Fatal("expected handler")
	}
	if m.Lookup("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	stats := m.Stats()
	if stats["route1"] == nil {
		t.Fatal("expected stats for route1")
	}
}
