package cache

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
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

func TestSharedBucket_SameStore(t *testing.T) {
	cbr := NewCacheByRoute(nil)

	cbr.AddRoute("route1", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
		Bucket:  "shared",
	})
	cbr.AddRoute("route2", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
		Bucket:  "shared",
	})

	h1 := cbr.GetHandler("route1")
	h2 := cbr.GetHandler("route2")

	// Store via route1
	req := httptest.NewRequest("GET", "/api/data", nil)
	h1.Store(req, &Entry{StatusCode: 200, Body: []byte(`{"from":"route1"}`)})

	// Should be retrievable via route2 (same bucket = same store)
	got, ok := h2.Get(req)
	if !ok {
		t.Fatal("expected cache hit via shared bucket")
	}
	if string(got.Body) != `{"from":"route1"}` {
		t.Errorf("expected body from route1, got %s", got.Body)
	}
}

func TestSharedBucket_DifferentBucket(t *testing.T) {
	cbr := NewCacheByRoute(nil)

	cbr.AddRoute("route1", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
		Bucket:  "bucket_a",
	})
	cbr.AddRoute("route2", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
		Bucket:  "bucket_b",
	})

	h1 := cbr.GetHandler("route1")
	h2 := cbr.GetHandler("route2")

	req := httptest.NewRequest("GET", "/api/data", nil)
	h1.Store(req, &Entry{StatusCode: 200, Body: []byte(`{"from":"route1"}`)})

	// Different bucket = isolated stores
	_, ok := h2.Get(req)
	if ok {
		t.Error("expected cache miss for different bucket")
	}
}

func TestSharedBucket_NoBucket(t *testing.T) {
	cbr := NewCacheByRoute(nil)

	cbr.AddRoute("route1", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})
	cbr.AddRoute("route2", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	h1 := cbr.GetHandler("route1")
	h2 := cbr.GetHandler("route2")

	req := httptest.NewRequest("GET", "/api/data", nil)
	h1.Store(req, &Entry{StatusCode: 200, Body: []byte(`{"from":"route1"}`)})

	// No bucket = per-route isolation (existing behavior)
	_, ok := h2.Get(req)
	if ok {
		t.Error("expected cache miss for per-route isolation")
	}
}

func TestSharedBucket_Stats(t *testing.T) {
	cbr := NewCacheByRoute(nil)

	cbr.AddRoute("route1", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
		Bucket:  "shared",
	})
	cbr.AddRoute("route2", config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	stats := cbr.Stats()
	if stats["route1"].Bucket != "shared" {
		t.Errorf("expected bucket=shared for route1, got %q", stats["route1"].Bucket)
	}
	if stats["route2"].Bucket != "" {
		t.Errorf("expected empty bucket for route2, got %q", stats["route2"].Bucket)
	}
}

func TestCacheByRoute_PurgeRoute(t *testing.T) {
	cbr := NewCacheByRoute(nil)
	cbr.AddRoute("route1", config.CacheConfig{Enabled: true, MaxSize: 100})

	h := cbr.GetHandler("route1")
	req := httptest.NewRequest("GET", "/api/data", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte("cached")})

	size, ok := cbr.PurgeRoute("route1")
	if !ok {
		t.Fatal("expected route found")
	}
	if size != 1 {
		t.Errorf("expected 1 entry purged, got %d", size)
	}

	// Verify cache is empty
	if _, hit := h.Get(req); hit {
		t.Error("expected cache miss after purge")
	}
}

func TestCacheByRoute_PurgeRouteKey(t *testing.T) {
	cbr := NewCacheByRoute(nil)
	cbr.AddRoute("route1", config.CacheConfig{Enabled: true, MaxSize: 100})

	h := cbr.GetHandler("route1")
	req := httptest.NewRequest("GET", "/api/data", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte("cached")})

	key := h.BuildKey(req, h.keyHeaders)
	ok := cbr.PurgeRouteKey("route1", key)
	if !ok {
		t.Fatal("expected route found")
	}

	if _, hit := h.Get(req); hit {
		t.Error("expected cache miss after key purge")
	}
}

func TestCacheByRoute_PurgeAll(t *testing.T) {
	cbr := NewCacheByRoute(nil)
	cbr.AddRoute("route1", config.CacheConfig{Enabled: true, MaxSize: 100})
	cbr.AddRoute("route2", config.CacheConfig{Enabled: true, MaxSize: 100})

	h1 := cbr.GetHandler("route1")
	h2 := cbr.GetHandler("route2")
	req := httptest.NewRequest("GET", "/api/data", nil)
	h1.Store(req, &Entry{StatusCode: 200, Body: []byte("r1")})
	h2.Store(req, &Entry{StatusCode: 200, Body: []byte("r2")})

	total := cbr.PurgeAll()
	if total != 2 {
		t.Errorf("expected 2 entries purged, got %d", total)
	}

	if _, hit := h1.Get(req); hit {
		t.Error("expected cache miss on route1 after purge all")
	}
	if _, hit := h2.Get(req); hit {
		t.Error("expected cache miss on route2 after purge all")
	}
}

func TestCacheByRoute_PurgeRoute_NotFound(t *testing.T) {
	cbr := NewCacheByRoute(nil)
	_, ok := cbr.PurgeRoute("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent route")
	}
}

func TestCacheByRoute_PurgeRouteKey_NotFound(t *testing.T) {
	cbr := NewCacheByRoute(nil)
	ok := cbr.PurgeRouteKey("nonexistent", "somekey")
	if ok {
		t.Error("expected not found for nonexistent route")
	}
}

func TestGetWithStaleness_Fresh(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:              true,
		MaxSize:              100,
		TTL:                  10 * time.Second,
		StaleWhileRevalidate: 5 * time.Second,
		StaleIfError:         5 * time.Second,
	})

	req := httptest.NewRequest("GET", "/api/data", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte(`fresh`)})

	key := h.KeyForRequest(req)
	entry, fresh, stale := h.GetWithStaleness(key)
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if !fresh {
		t.Error("expected fresh=true")
	}
	if stale {
		t.Error("expected stale=false")
	}
}

func TestGetWithStaleness_Stale(t *testing.T) {
	// Use very short TTL so we can test staleness quickly
	cfg := config.CacheConfig{
		Enabled:              true,
		MaxSize:              100,
		TTL:                  50 * time.Millisecond,
		StaleWhileRevalidate: 5 * time.Second,
		StaleIfError:         5 * time.Second,
	}
	store := NewMemoryStore(100, cfg.TTL+5*time.Second) // extended TTL for store
	h := NewHandler(cfg, store)

	req := httptest.NewRequest("GET", "/api/data", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte(`stale data`)})

	// Wait for fresh TTL to expire
	time.Sleep(80 * time.Millisecond)

	key := h.KeyForRequest(req)
	entry, fresh, stale := h.GetWithStaleness(key)
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if fresh {
		t.Error("expected fresh=false")
	}
	if !stale {
		t.Error("expected stale=true")
	}
	if string(entry.Body) != `stale data` {
		t.Errorf("expected body 'stale data', got %s", entry.Body)
	}
}

func TestGetWithStaleness_Expired(t *testing.T) {
	// Both TTL and stale windows are very short
	cfg := config.CacheConfig{
		Enabled:              true,
		MaxSize:              100,
		TTL:                  30 * time.Millisecond,
		StaleWhileRevalidate: 30 * time.Millisecond,
		StaleIfError:         30 * time.Millisecond,
	}
	store := NewMemoryStore(100, cfg.TTL+30*time.Millisecond) // extended TTL for store
	h := NewHandler(cfg, store)

	req := httptest.NewRequest("GET", "/api/data", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte(`old data`)})

	// Wait for both TTL and stale window to expire
	time.Sleep(100 * time.Millisecond)

	key := h.KeyForRequest(req)
	entry, fresh, stale := h.GetWithStaleness(key)
	if entry != nil {
		t.Error("expected nil entry after full expiry")
	}
	if fresh {
		t.Error("expected fresh=false")
	}
	if stale {
		t.Error("expected stale=false")
	}
}

func TestGetWithStaleness_Miss(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:              true,
		MaxSize:              100,
		StaleWhileRevalidate: 5 * time.Second,
	})

	entry, fresh, stale := h.GetWithStaleness("nonexistent")
	if entry != nil {
		t.Error("expected nil entry for miss")
	}
	if fresh || stale {
		t.Error("expected fresh=false, stale=false for miss")
	}
}

func TestStoreByKey(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	key := "test-key"
	h.StoreByKey(key, &Entry{StatusCode: 200, Body: []byte("stored")})

	entry, fresh, stale := h.GetWithStaleness(key)
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if !fresh {
		t.Error("expected fresh=true")
	}
	if stale {
		t.Error("expected stale=false")
	}
	if entry.StoredAt.IsZero() {
		t.Error("expected StoredAt to be set")
	}
}

func TestStoreSetStoredAt(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	req := httptest.NewRequest("GET", "/api/data", nil)
	entry := &Entry{StatusCode: 200, Body: []byte("test")}
	if !entry.StoredAt.IsZero() {
		t.Error("expected StoredAt to be zero before Store")
	}

	h.Store(req, entry)

	if entry.StoredAt.IsZero() {
		t.Error("expected StoredAt to be set after Store")
	}
}

func TestHandlerHasStaleSupport(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.CacheConfig
		want bool
	}{
		{
			name: "no stale support",
			cfg:  config.CacheConfig{Enabled: true},
			want: false,
		},
		{
			name: "swr only",
			cfg:  config.CacheConfig{Enabled: true, StaleWhileRevalidate: 5 * time.Second},
			want: true,
		},
		{
			name: "sie only",
			cfg:  config.CacheConfig{Enabled: true, StaleIfError: 5 * time.Second},
			want: true,
		},
		{
			name: "both",
			cfg:  config.CacheConfig{Enabled: true, StaleWhileRevalidate: 5 * time.Second, StaleIfError: 10 * time.Second},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHandler(tt.cfg)
			if got := h.HasStaleSupport(); got != tt.want {
				t.Errorf("HasStaleSupport() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRevalidatingDedup(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:              true,
		StaleWhileRevalidate: 5 * time.Second,
	})

	key := "test-key"

	// First call should return false (not already revalidating)
	if h.IsRevalidating(key) {
		t.Error("expected IsRevalidating to return false on first call")
	}

	// Second call should return true (already in-flight)
	if !h.IsRevalidating(key) {
		t.Error("expected IsRevalidating to return true on second call")
	}

	// After DoneRevalidating, should be available again
	h.DoneRevalidating(key)
	if h.IsRevalidating(key) {
		t.Error("expected IsRevalidating to return false after DoneRevalidating")
	}

	// Clean up
	h.DoneRevalidating(key)
}

func TestCapturingResponseWriter(t *testing.T) {
	crw := NewCapturingResponseWriter()

	crw.Header().Set("Content-Type", "text/plain")
	crw.WriteHeader(201)
	crw.Write([]byte("hello"))

	if crw.StatusCode() != 201 {
		t.Errorf("expected status 201, got %d", crw.StatusCode())
	}
	if crw.Body.String() != "hello" {
		t.Errorf("expected body 'hello', got '%s'", crw.Body.String())
	}
	if crw.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("expected Content-Type: text/plain, got %s", crw.Header().Get("Content-Type"))
	}
}

func TestCapturingResponseWriter_DefaultStatus(t *testing.T) {
	crw := NewCapturingResponseWriter()
	crw.Write([]byte("body"))

	// Should default to 200
	if crw.StatusCode() != 200 {
		t.Errorf("expected default status 200, got %d", crw.StatusCode())
	}
}

func TestCapturingResponseWriter_DoubleWriteHeader(t *testing.T) {
	crw := NewCapturingResponseWriter()
	crw.WriteHeader(201)
	crw.WriteHeader(500) // should be ignored

	if crw.StatusCode() != 201 {
		t.Errorf("expected status 201 (first), got %d", crw.StatusCode())
	}
}

func TestStaleWindow_SWRLargerThanSIE(t *testing.T) {
	cfg := config.CacheConfig{
		Enabled:              true,
		MaxSize:              100,
		TTL:                  50 * time.Millisecond,
		StaleWhileRevalidate: 10 * time.Second,
		StaleIfError:         2 * time.Second,
	}
	store := NewMemoryStore(100, cfg.TTL+10*time.Second)
	h := NewHandler(cfg, store)

	req := httptest.NewRequest("GET", "/test", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte("data")})

	// Wait for fresh TTL to expire
	time.Sleep(80 * time.Millisecond)

	key := h.KeyForRequest(req)
	entry, fresh, stale := h.GetWithStaleness(key)
	if entry == nil || fresh || !stale {
		t.Error("expected stale entry within SWR window")
	}
}

func TestStaleWindow_SIELargerThanSWR(t *testing.T) {
	cfg := config.CacheConfig{
		Enabled:              true,
		MaxSize:              100,
		TTL:                  50 * time.Millisecond,
		StaleWhileRevalidate: 2 * time.Second,
		StaleIfError:         10 * time.Second,
	}
	store := NewMemoryStore(100, cfg.TTL+10*time.Second)
	h := NewHandler(cfg, store)

	req := httptest.NewRequest("GET", "/test", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte("data")})

	// Wait for fresh TTL to expire
	time.Sleep(80 * time.Millisecond)

	key := h.KeyForRequest(req)
	entry, fresh, stale := h.GetWithStaleness(key)
	if entry == nil || fresh || !stale {
		t.Error("expected stale entry within SIE window")
	}
}

func TestCacheByRoute_ExtendedStoreTTL(t *testing.T) {
	// Verify that the store TTL is extended to cover the stale window
	cbr := NewCacheByRoute(nil)

	cbr.AddRoute("route1", config.CacheConfig{
		Enabled:              true,
		MaxSize:              100,
		TTL:                  50 * time.Millisecond,
		StaleWhileRevalidate: 5 * time.Second,
	})

	h := cbr.GetHandler("route1")
	if h == nil {
		t.Fatal("expected handler for route1")
	}

	// Store an entry
	req := httptest.NewRequest("GET", "/test", nil)
	h.Store(req, &Entry{StatusCode: 200, Body: []byte("test")})

	// Wait for fresh TTL to expire but within stale window
	time.Sleep(80 * time.Millisecond)

	// The entry should still be retrievable (store TTL extended)
	key := h.KeyForRequest(req)
	entry, fresh, stale := h.GetWithStaleness(key)
	if entry == nil {
		t.Fatal("expected entry to be present (store TTL should be extended)")
	}
	if fresh {
		t.Error("expected fresh=false")
	}
	if !stale {
		t.Error("expected stale=true")
	}
}

func TestKeyForRequest(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:    true,
		MaxSize:    100,
		KeyHeaders: []string{"Accept"},
	})

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Accept", "application/json")

	key1 := h.KeyForRequest(req)
	key2 := h.BuildKey(req, h.keyHeaders)

	if key1 != key2 {
		t.Error("KeyForRequest should produce same key as BuildKey with handler's keyHeaders")
	}
}

func TestHandler_StoreWithMeta_PathIndex(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	key1 := "key1"
	key2 := "key2"
	h.StoreWithMeta(key1, "/api/users", &Entry{StatusCode: 200, Body: []byte("users")})
	h.StoreWithMeta(key2, "/api/posts", &Entry{StatusCode: 200, Body: []byte("posts")})

	// Both should be retrievable
	if _, ok := h.cache.Get(key1); !ok {
		t.Error("expected key1 in cache")
	}
	if _, ok := h.cache.Get(key2); !ok {
		t.Error("expected key2 in cache")
	}
}

func TestHandler_StoreWithMeta_TagExtraction(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:    true,
		MaxSize:    100,
		TagHeaders: []string{"Cache-Tag", "Surrogate-Key"},
		Tags:       []string{"static-tag"},
	})

	entry := &Entry{
		StatusCode: 200,
		Headers:    http.Header{"Cache-Tag": {"product listing"}, "Surrogate-Key": {"home"}},
		Body:       []byte("data"),
	}
	h.StoreWithMeta("key1", "/page", entry)

	// Entry should have tags
	if len(entry.Tags) != 4 {
		t.Errorf("expected 4 tags (static-tag + product + listing + home), got %d: %v", len(entry.Tags), entry.Tags)
	}
}

func TestHandler_PurgeByPathPattern(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	h.StoreWithMeta("key1", "/api/users", &Entry{StatusCode: 200, Body: []byte("users")})
	h.StoreWithMeta("key2", "/api/posts", &Entry{StatusCode: 200, Body: []byte("posts")})
	h.StoreWithMeta("key3", "/static/style.css", &Entry{StatusCode: 200, Body: []byte("css")})

	count := h.PurgeByPathPattern("/api/*")
	if count != 2 {
		t.Errorf("expected 2 purged, got %d", count)
	}

	// /static entry should still exist
	if _, ok := h.cache.Get("key3"); !ok {
		t.Error("expected key3 to still exist")
	}

	// api entries should be gone
	if _, ok := h.cache.Get("key1"); ok {
		t.Error("expected key1 to be purged")
	}
	if _, ok := h.cache.Get("key2"); ok {
		t.Error("expected key2 to be purged")
	}
}

func TestHandler_PurgeByPathPattern_NoMatch(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	h.StoreWithMeta("key1", "/api/users", &Entry{StatusCode: 200, Body: []byte("users")})

	count := h.PurgeByPathPattern("/nonexistent/*")
	if count != 0 {
		t.Errorf("expected 0 purged, got %d", count)
	}
}

func TestHandler_PurgeByTags(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
		Tags:    []string{"route-tag"},
	})

	h.StoreWithMeta("key1", "/page1", &Entry{StatusCode: 200, Body: []byte("1")})
	h.StoreWithMeta("key2", "/page2", &Entry{StatusCode: 200, Body: []byte("2")})

	count := h.PurgeByTags([]string{"route-tag"})
	if count != 2 {
		t.Errorf("expected 2 purged, got %d", count)
	}

	if _, ok := h.cache.Get("key1"); ok {
		t.Error("expected key1 to be purged")
	}
}

func TestHandler_PurgeByTags_WithHeaderTags(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled:    true,
		MaxSize:    100,
		TagHeaders: []string{"Cache-Tag"},
	})

	h.StoreWithMeta("key1", "/page1", &Entry{
		StatusCode: 200,
		Headers:    http.Header{"Cache-Tag": {"product"}},
		Body:       []byte("1"),
	})
	h.StoreWithMeta("key2", "/page2", &Entry{
		StatusCode: 200,
		Headers:    http.Header{"Cache-Tag": {"user"}},
		Body:       []byte("2"),
	})

	count := h.PurgeByTags([]string{"product"})
	if count != 1 {
		t.Errorf("expected 1 purged, got %d", count)
	}

	if _, ok := h.cache.Get("key1"); ok {
		t.Error("expected key1 to be purged")
	}
	if _, ok := h.cache.Get("key2"); !ok {
		t.Error("expected key2 to still exist")
	}
}

func TestHandler_MultipleKeysPerPath(t *testing.T) {
	h := newTestHandler(config.CacheConfig{
		Enabled: true,
		MaxSize: 100,
	})

	// Same path, different keys (e.g., different query strings reflected in key)
	h.StoreWithMeta("key1", "/api/users", &Entry{StatusCode: 200, Body: []byte("json")})
	h.StoreWithMeta("key2", "/api/users", &Entry{StatusCode: 200, Body: []byte("xml")})

	count := h.PurgeByPathPattern("/api/users")
	if count != 2 {
		t.Errorf("expected 2 purged for same path, got %d", count)
	}
}

func TestCacheByRoute_PurgeByPathPattern(t *testing.T) {
	cbr := NewCacheByRoute(nil)
	cbr.AddRoute("route1", config.CacheConfig{Enabled: true, MaxSize: 100})

	h := cbr.GetHandler("route1")
	h.StoreWithMeta("key1", "/api/users", &Entry{StatusCode: 200, Body: []byte("users")})
	h.StoreWithMeta("key2", "/api/posts", &Entry{StatusCode: 200, Body: []byte("posts")})

	count, ok := cbr.PurgeByPathPattern("route1", "/api/*")
	if !ok {
		t.Fatal("expected route found")
	}
	if count != 2 {
		t.Errorf("expected 2 purged, got %d", count)
	}

	// Not found
	_, ok = cbr.PurgeByPathPattern("nonexistent", "/api/*")
	if ok {
		t.Error("expected not found for nonexistent route")
	}
}

func TestCacheByRoute_PurgeByTags(t *testing.T) {
	cbr := NewCacheByRoute(nil)
	cbr.AddRoute("route1", config.CacheConfig{Enabled: true, MaxSize: 100, Tags: []string{"all"}})

	h := cbr.GetHandler("route1")
	h.StoreWithMeta("key1", "/page1", &Entry{StatusCode: 200, Body: []byte("1")})
	h.StoreWithMeta("key2", "/page2", &Entry{StatusCode: 200, Body: []byte("2")})

	count, ok := cbr.PurgeByTags("route1", []string{"all"})
	if !ok {
		t.Fatal("expected route found")
	}
	if count != 2 {
		t.Errorf("expected 2 purged, got %d", count)
	}

	// Not found
	_, ok = cbr.PurgeByTags("nonexistent", []string{"all"})
	if ok {
		t.Error("expected not found for nonexistent route")
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
