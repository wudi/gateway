package cache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
)

func newTestHandler(cfg config.CacheConfig) *Handler {
	maxSize := cfg.MaxSize
	if maxSize <= 0 {
		maxSize = 1000
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	return NewHandler(cfg, NewMemoryStore(maxSize, ttl))
}

func TestHandlerShouldCache(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		Methods: []string{"GET"},
	})

	tests := []struct {
		name    string
		method  string
		headers map[string]string
		want    bool
	}{
		{"GET request", "GET", nil, true},
		{"POST request", "POST", nil, false},
		{"GET with no-store", "GET", map[string]string{"Cache-Control": "no-store"}, false},
		{"GET with no-cache", "GET", map[string]string{"Cache-Control": "no-cache"}, false},
		{"GET with max-age", "GET", map[string]string{"Cache-Control": "max-age=60"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			got := h.ShouldCache(req)
			if got != tt.want {
				t.Errorf("ShouldCache() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandlerShouldStore(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:     true,
		MaxBodySize: 1024,
	})

	tests := []struct {
		name       string
		statusCode int
		headers    http.Header
		bodySize   int64
		want       bool
	}{
		{"200 OK", 200, http.Header{}, 100, true},
		{"201 Created", 201, http.Header{}, 100, true},
		{"404 Not Found", 404, http.Header{}, 100, false},
		{"500 Error", 500, http.Header{}, 100, false},
		{"200 with no-store", 200, http.Header{"Cache-Control": {"no-store"}}, 100, false},
		{"200 body too large", 200, http.Header{}, 2048, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := h.ShouldStore(tt.statusCode, tt.headers, tt.bodySize)
			if got != tt.want {
				t.Errorf("ShouldStore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandlerBuildKey(t *testing.T) {
	h := newTestHandler(config.CacheConfig{Enabled: true})

	req1 := httptest.NewRequest("GET", "/api/users", nil)
	req2 := httptest.NewRequest("GET", "/api/users", nil)
	req3 := httptest.NewRequest("GET", "/api/posts", nil)
	req4 := httptest.NewRequest("POST", "/api/users", nil)

	key1 := h.BuildKey(req1, nil)
	key2 := h.BuildKey(req2, nil)
	key3 := h.BuildKey(req3, nil)
	key4 := h.BuildKey(req4, nil)

	if key1 != key2 {
		t.Error("same requests should produce same key")
	}
	if key1 == key3 {
		t.Error("different paths should produce different keys")
	}
	if key1 == key4 {
		t.Error("different methods should produce different keys")
	}
}

func TestHandlerBuildKeyWithHeaders(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:    true,
		KeyHeaders: []string{"Accept", "Authorization"},
	})

	req1 := httptest.NewRequest("GET", "/api/users", nil)
	req1.Header.Set("Accept", "application/json")

	req2 := httptest.NewRequest("GET", "/api/users", nil)
	req2.Header.Set("Accept", "text/html")

	key1 := h.BuildKey(req1, h.keyHeaders)
	key2 := h.BuildKey(req2, h.keyHeaders)

	if key1 == key2 {
		t.Error("different headers should produce different keys")
	}
}

func TestHandlerGetAndStore(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	req := httptest.NewRequest("GET", "/api/users", nil)

	// Miss
	_, ok := h.Get(req)
	if ok {
		t.Fatal("expected cache miss")
	}

	// Store
	entry := &Entry{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`[{"id":1}]`),
	}
	h.Store(req, entry)

	// Hit
	got, ok := h.Get(req)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.StatusCode != 200 {
		t.Errorf("expected 200, got %d", got.StatusCode)
	}
}

func TestWriteCachedResponse(t *testing.T) {
	entry := &Entry{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"cached":true}`),
	}

	w := httptest.NewRecorder()
	WriteCachedResponse(w, entry)

	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT, got %s", w.Header().Get("X-Cache"))
	}
	if w.Body.String() != `{"cached":true}` {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
}

func TestCachingResponseWriter(t *testing.T) {
	w := httptest.NewRecorder()
	crw := NewCachingResponseWriter(w)

	crw.Header().Set("Content-Type", "text/plain")
	crw.WriteHeader(201)
	crw.Write([]byte("hello"))

	if crw.StatusCode() != 201 {
		t.Errorf("expected status 201, got %d", crw.StatusCode())
	}
	if crw.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got '%s'", crw.Body.String())
	}
	if w.Code != 201 {
		t.Errorf("expected underlying writer status 201, got %d", w.Code)
	}
	if w.Body.String() != "hello" {
		t.Errorf("expected underlying writer body 'hello', got '%s'", w.Body.String())
	}
}

func TestIsMutatingMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{"GET", false},
		{"HEAD", false},
		{"OPTIONS", false},
		{"POST", true},
		{"PUT", true},
		{"PATCH", true},
		{"DELETE", true},
	}

	for _, tt := range tests {
		got := IsMutatingMethod(tt.method)
		if got != tt.want {
			t.Errorf("IsMutatingMethod(%s) = %v, want %v", tt.method, got, tt.want)
		}
	}
}

func TestCacheByRoute(t *testing.T) {
	cbr := NewCacheByRoute(nil)

	cbr.AddRoute("route1", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})
	cbr.AddRoute("route2", config.CacheConfig{
		Enabled: true,
		MaxSize: 50,
	})

	h1 := cbr.GetHandler("route1")
	if h1 == nil {
		t.Fatal("expected handler for route1")
	}

	h2 := cbr.GetHandler("route2")
	if h2 == nil {
		t.Fatal("expected handler for route2")
	}

	h3 := cbr.GetHandler("route3")
	if h3 != nil {
		t.Fatal("expected nil for non-existent route3")
	}

	stats := cbr.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 route stats, got %d", len(stats))
	}
}

func TestCacheByRouteDistributedFallback(t *testing.T) {
	// When no Redis client is configured, distributed mode falls back to local
	cbr := NewCacheByRoute(nil)

	cbr.AddRoute("route1", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
		Mode:    "distributed",
	})

	h := cbr.GetHandler("route1")
	if h == nil {
		t.Fatal("expected handler for route1 even with distributed mode and no redis")
	}

	// Should work as a regular local cache
	req := httptest.NewRequest("GET", "/test", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte("ok")})
	got, ok := h.Get(req)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.StatusCode != 200 {
		t.Errorf("expected 200, got %d", got.StatusCode)
	}
}
