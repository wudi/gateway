package etag

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func expectedETag(body []byte, weak bool) string {
	sum := sha256.Sum256(body)
	tag := `"` + hex.EncodeToString(sum[:16]) + `"`
	if weak {
		tag = "W/" + tag
	}
	return tag
}

func TestETagGeneration(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})
	body := []byte(`{"message":"hello"}`)

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	etag := rec.Header().Get("ETag")
	want := expectedETag(body, false)
	if etag != want {
		t.Errorf("expected ETag %q, got %q", want, etag)
	}
	if rec.Body.String() != string(body) {
		t.Errorf("expected body %q, got %q", string(body), rec.Body.String())
	}
}

func TestStrongETag(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true, Weak: false})
	body := []byte("strong-body")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	etag := rec.Header().Get("ETag")
	if len(etag) < 2 || etag[:2] == "W/" {
		t.Errorf("expected strong ETag (no W/ prefix), got %q", etag)
	}
	want := expectedETag(body, false)
	if etag != want {
		t.Errorf("expected ETag %q, got %q", want, etag)
	}
}

func TestWeakETag(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true, Weak: true})
	body := []byte("weak-body")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	etag := rec.Header().Get("ETag")
	if len(etag) < 2 || etag[:2] != "W/" {
		t.Errorf("expected weak ETag (W/ prefix), got %q", etag)
	}
	want := expectedETag(body, true)
	if etag != want {
		t.Errorf("expected ETag %q, got %q", want, etag)
	}
}

func TestNotModifiedWhenIfNoneMatchMatches(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})
	body := []byte("conditional-body")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	// First request to get the ETag.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec1, req1)

	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header in first response")
	}

	// Second request with If-None-Match set to the returned ETag.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Errorf("expected 304, got %d", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Errorf("expected empty body for 304, got %q", rec2.Body.String())
	}
}

func TestNormalResponseWhenIfNoneMatchDiffers(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})
	body := []byte("some-body")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", `"completely-different-etag"`)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if rec.Body.String() != string(body) {
		t.Errorf("expected body %q, got %q", string(body), rec.Body.String())
	}
}

func TestIfNoneMatchWildcard(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})
	body := []byte("wildcard-body")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("If-None-Match", "*")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Errorf("expected 304 for wildcard If-None-Match, got %d", rec.Code)
	}
}

func TestWeakETagNotModified(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true, Weak: true})
	body := []byte("weak-conditional")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	// First request.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec1, req1)

	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header")
	}

	// Second request with the weak ETag.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Errorf("expected 304, got %d", rec2.Code)
	}
}

func TestStatsTracking(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})
	body := []byte("stats-body")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	// First request generates an ETag.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec1, req1)

	etag := rec1.Header().Get("ETag")

	// Second request with matching If-None-Match.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("If-None-Match", etag)
	handler.ServeHTTP(rec2, req2)

	if h.Generated() != 2 {
		t.Errorf("expected 2 generated, got %d", h.Generated())
	}
	if h.NotModified() != 1 {
		t.Errorf("expected 1 not_modified, got %d", h.NotModified())
	}
}

func TestETagByRoute(t *testing.T) {
	m := NewETagByRoute()
	m.AddRoute("route1", config.ETagConfig{Enabled: true})

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

func TestNoETagForEmptyBody(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/resource", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("expected no ETag for empty body, got %q", etag)
	}
	if h.Generated() != 0 {
		t.Errorf("expected 0 generated for empty body, got %d", h.Generated())
	}
}

func TestNoETagForErrorResponse(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", rec.Code)
	}
	if etag := rec.Header().Get("ETag"); etag != "" {
		t.Errorf("expected no ETag for error response, got %q", etag)
	}
}

func TestIfNoneMatchCommaList(t *testing.T) {
	h := New(config.ETagConfig{Enabled: true})
	body := []byte("list-body")

	handler := h.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))

	// Get the ETag.
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec1, req1)
	etag := rec1.Header().Get("ETag")

	// Send a comma-separated list including the matching ETag.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("If-None-Match", `"other-etag", `+etag+`, "another"`)
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotModified {
		t.Errorf("expected 304 for comma-separated If-None-Match, got %d", rec2.Code)
	}
}
