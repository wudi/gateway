package cache

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
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
	r := httptest.NewRequest("GET", "/test", nil)
	notModified := WriteCachedResponse(w, r, entry, false)

	if notModified {
		t.Error("expected notModified=false")
	}
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

func TestGenerateETag(t *testing.T) {
	body1 := []byte("hello world")
	body2 := []byte("hello world")
	body3 := []byte("different body")

	etag1 := GenerateETag(body1)
	etag2 := GenerateETag(body2)
	etag3 := GenerateETag(body3)

	// Deterministic
	if etag1 != etag2 {
		t.Errorf("same body should produce same ETag: %s vs %s", etag1, etag2)
	}

	// Different bodies produce different ETags
	if etag1 == etag3 {
		t.Error("different bodies should produce different ETags")
	}

	// Quoted format
	if !strings.HasPrefix(etag1, `"`) || !strings.HasSuffix(etag1, `"`) {
		t.Errorf("ETag should be quoted, got: %s", etag1)
	}

	// Should be 32 hex chars + 2 quotes = 34 chars (16 bytes hex-encoded)
	if len(etag1) != 34 {
		t.Errorf("expected ETag length 34, got %d: %s", len(etag1), etag1)
	}
}

func TestPopulateConditionalFields(t *testing.T) {
	t.Run("generated values", func(t *testing.T) {
		entry := &Entry{
			StatusCode: 200,
			Headers:    http.Header{"Content-Type": {"application/json"}},
			Body:       []byte(`{"key":"value"}`),
		}
		PopulateConditionalFields(entry)

		if entry.ETag == "" {
			t.Error("expected ETag to be generated")
		}
		if entry.ETag != GenerateETag(entry.Body) {
			t.Error("expected ETag to match GenerateETag output")
		}
		if entry.LastModified.IsZero() {
			t.Error("expected LastModified to be set")
		}
		// Should be truncated to seconds
		if entry.LastModified.Nanosecond() != 0 {
			t.Error("expected LastModified truncated to seconds")
		}
	})

	t.Run("backend-provided ETag", func(t *testing.T) {
		entry := &Entry{
			StatusCode: 200,
			Headers:    http.Header{"Etag": {`"backend-etag-123"`}},
			Body:       []byte(`{"key":"value"}`),
		}
		PopulateConditionalFields(entry)

		if entry.ETag != `"backend-etag-123"` {
			t.Errorf("expected backend ETag, got: %s", entry.ETag)
		}
	})

	t.Run("backend-provided Last-Modified", func(t *testing.T) {
		lm := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
		entry := &Entry{
			StatusCode: 200,
			Headers:    http.Header{"Last-Modified": {lm.Format(http.TimeFormat)}},
			Body:       []byte(`data`),
		}
		PopulateConditionalFields(entry)

		if !entry.LastModified.Equal(lm) {
			t.Errorf("expected backend Last-Modified %v, got %v", lm, entry.LastModified)
		}
	})
}

func TestCheckConditional_IfNoneMatch(t *testing.T) {
	entry := &Entry{
		ETag:         `"abc123"`,
		LastModified: time.Now().Truncate(time.Second),
	}

	tests := []struct {
		name string
		inm  string
		want bool
	}{
		{"exact match", `"abc123"`, true},
		{"no match", `"xyz789"`, false},
		{"wildcard", "*", true},
		{"comma-separated match", `"xyz789", "abc123"`, true},
		{"comma-separated no match", `"xyz789", "def456"`, false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/test", nil)
			if tt.inm != "" {
				r.Header.Set("If-None-Match", tt.inm)
			}
			got := CheckConditional(r, entry)
			if got != tt.want {
				t.Errorf("CheckConditional() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckConditional_IfModifiedSince(t *testing.T) {
	entryTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	entry := &Entry{
		ETag:         `"etag1"`,
		LastModified: entryTime,
	}

	tests := []struct {
		name string
		ims  string
		want bool
	}{
		{"fresh - same time", entryTime.Format(http.TimeFormat), true},
		{"fresh - later time", entryTime.Add(time.Hour).Format(http.TimeFormat), true},
		{"stale - earlier time", entryTime.Add(-time.Hour).Format(http.TimeFormat), false},
		{"malformed date", "not-a-date", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/test", nil)
			r.Header.Set("If-Modified-Since", tt.ims)
			got := CheckConditional(r, entry)
			if got != tt.want {
				t.Errorf("CheckConditional() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckConditional_Precedence(t *testing.T) {
	entryTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	entry := &Entry{
		ETag:         `"etag1"`,
		LastModified: entryTime,
	}

	// If-None-Match takes precedence: ETag doesn't match, but If-Modified-Since would say fresh
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("If-None-Match", `"wrong-etag"`)
	r.Header.Set("If-Modified-Since", entryTime.Add(time.Hour).Format(http.TimeFormat))

	got := CheckConditional(r, entry)
	if got {
		t.Error("expected false: If-None-Match should take precedence and it doesn't match")
	}
}

func TestCheckConditional_NoHeaders(t *testing.T) {
	entry := &Entry{
		ETag:         `"etag1"`,
		LastModified: time.Now().Truncate(time.Second),
	}

	r := httptest.NewRequest("GET", "/test", nil)
	got := CheckConditional(r, entry)
	if got {
		t.Error("expected false when no conditional headers present")
	}
}

func TestWriteCachedResponse_Conditional304(t *testing.T) {
	entry := &Entry{
		StatusCode:   200,
		Headers:      http.Header{"Content-Type": {"application/json"}},
		Body:         []byte(`{"data":"large payload"}`),
		ETag:         `"test-etag"`,
		LastModified: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("If-None-Match", `"test-etag"`)

	notModified := WriteCachedResponse(w, r, entry, true)

	if !notModified {
		t.Error("expected notModified=true")
	}
	if w.Code != 304 {
		t.Errorf("expected 304, got %d", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected empty body for 304, got %d bytes", w.Body.Len())
	}
	if w.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT, got %s", w.Header().Get("X-Cache"))
	}
	if w.Header().Get("ETag") != `"test-etag"` {
		t.Errorf("expected ETag header, got %s", w.Header().Get("ETag"))
	}
	if w.Header().Get("Last-Modified") == "" {
		t.Error("expected Last-Modified header")
	}
}

func TestWriteCachedResponse_Conditional200(t *testing.T) {
	entry := &Entry{
		StatusCode:   200,
		Headers:      http.Header{"Content-Type": {"application/json"}},
		Body:         []byte(`{"data":"payload"}`),
		ETag:         `"test-etag"`,
		LastModified: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	// No conditional headers

	notModified := WriteCachedResponse(w, r, entry, true)

	if notModified {
		t.Error("expected notModified=false")
	}
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != `{"data":"payload"}` {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
	if w.Header().Get("ETag") != `"test-etag"` {
		t.Errorf("expected ETag header, got %s", w.Header().Get("ETag"))
	}
}

func TestWriteCachedResponse_ConditionalDisabled(t *testing.T) {
	entry := &Entry{
		StatusCode:   200,
		Headers:      http.Header{"Content-Type": {"application/json"}},
		Body:         []byte(`{"data":"payload"}`),
		ETag:         `"test-etag"`,
		LastModified: time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/test", nil)
	r.Header.Set("If-None-Match", `"test-etag"`)

	// conditional=false: should always return full 200 even with matching headers
	notModified := WriteCachedResponse(w, r, entry, false)

	if notModified {
		t.Error("expected notModified=false when conditional disabled")
	}
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != `{"data":"payload"}` {
		t.Errorf("unexpected body: %s", w.Body.String())
	}
	// Should NOT inject ETag/Last-Modified when conditional is disabled
	if w.Header().Get("ETag") != "" {
		t.Error("expected no ETag header when conditional disabled")
	}
}

func TestCacheStatsNotModified(t *testing.T) {
	store := NewMemoryStore(100, time.Minute)
	c := New(store)

	stats := c.Stats()
	if stats.NotModifieds != 0 {
		t.Errorf("expected 0 not_modifieds, got %d", stats.NotModifieds)
	}

	c.RecordNotModified()
	c.RecordNotModified()
	c.RecordNotModified()

	stats = c.Stats()
	if stats.NotModifieds != 3 {
		t.Errorf("expected 3 not_modifieds, got %d", stats.NotModifieds)
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
