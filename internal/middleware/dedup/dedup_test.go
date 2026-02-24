package dedup

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestFingerprint(t *testing.T) {
	cd, err := New("test", config.RequestDedupConfig{
		Enabled:        true,
		IncludeHeaders: []string{"X-Custom"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cd.Close()

	r1 := httptest.NewRequest("POST", "/api/test?q=1", strings.NewReader("hello"))
	r1.Header.Set("X-Custom", "val1")

	fp1, err := cd.Fingerprint(r1)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == "" {
		t.Fatal("expected non-empty fingerprint")
	}

	// Same request should produce same fingerprint
	r2 := httptest.NewRequest("POST", "/api/test?q=1", strings.NewReader("hello"))
	r2.Header.Set("X-Custom", "val1")

	fp2, err := cd.Fingerprint(r2)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Errorf("same request got different fingerprints: %s vs %s", fp1, fp2)
	}

	// Different body should produce different fingerprint
	r3 := httptest.NewRequest("POST", "/api/test?q=1", strings.NewReader("world"))
	r3.Header.Set("X-Custom", "val1")

	fp3, err := cd.Fingerprint(r3)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == fp3 {
		t.Error("different body should produce different fingerprint")
	}

	// Different header should produce different fingerprint
	r4 := httptest.NewRequest("POST", "/api/test?q=1", strings.NewReader("hello"))
	r4.Header.Set("X-Custom", "val2")

	fp4, err := cd.Fingerprint(r4)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 == fp4 {
		t.Error("different header should produce different fingerprint")
	}
}

func TestFingerprintBodyRestored(t *testing.T) {
	cd, _ := New("test", config.RequestDedupConfig{Enabled: true}, nil)
	defer cd.Close()

	body := "test body content"
	r := httptest.NewRequest("POST", "/test", strings.NewReader(body))

	_, err := cd.Fingerprint(r)
	if err != nil {
		t.Fatal(err)
	}

	// Body should be restored
	restored, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != body {
		t.Errorf("body not restored: got %q, want %q", string(restored), body)
	}
}

func TestFingerprintNoBody(t *testing.T) {
	f := false
	cd, _ := New("test", config.RequestDedupConfig{
		Enabled:     true,
		IncludeBody: &f,
	}, nil)
	defer cd.Close()

	r1 := httptest.NewRequest("POST", "/test", strings.NewReader("hello"))
	r2 := httptest.NewRequest("POST", "/test", strings.NewReader("world"))

	fp1, _ := cd.Fingerprint(r1)
	fp2, _ := cd.Fingerprint(r2)

	if fp1 != fp2 {
		t.Error("with include_body=false, different bodies should produce same fingerprint")
	}
}

func TestMiddlewareDedup(t *testing.T) {
	cd, _ := New("test", config.RequestDedupConfig{
		Enabled: true,
		TTL:     5 * time.Second,
	}, nil)
	defer cd.Close()

	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("X-Backend", "true")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	})

	handler := cd.Middleware()(backend)

	// First request - should pass through
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(w1, r1)

	if callCount != 1 {
		t.Errorf("expected 1 backend call, got %d", callCount)
	}
	if w1.Code != 200 {
		t.Errorf("expected status 200, got %d", w1.Code)
	}
	if w1.Header().Get("X-Dedup-Replayed") != "" {
		t.Error("first request should not have X-Dedup-Replayed")
	}

	// Second identical request - should be served from cache
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(w2, r2)

	if callCount != 1 {
		t.Errorf("expected 1 backend call (deduped), got %d", callCount)
	}
	if w2.Header().Get("X-Dedup-Replayed") != "true" {
		t.Error("second request should have X-Dedup-Replayed=true")
	}
	if w2.Body.String() != `{"ok":true}` {
		t.Errorf("unexpected replayed body: %s", w2.Body.String())
	}

	// Different request - should pass through
	w3 := httptest.NewRecorder()
	r3 := httptest.NewRequest("GET", "/test?q=1", nil)
	handler.ServeHTTP(w3, r3)

	if callCount != 2 {
		t.Errorf("expected 2 backend calls, got %d", callCount)
	}
}

func TestMiddlewareConcurrentInflight(t *testing.T) {
	cd, _ := New("test", config.RequestDedupConfig{
		Enabled: true,
		TTL:     5 * time.Second,
	}, nil)
	defer cd.Close()

	callCount := int32(0)
	started := make(chan struct{})
	proceed := make(chan struct{})

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := sync.OnceFunc(func() { close(started) })
		n()
		<-proceed
		callCount++
		w.WriteHeader(200)
		w.Write([]byte("response"))
	})

	handler := cd.Middleware()(backend)

	var wg sync.WaitGroup

	// First request - will block in backend
	wg.Add(1)
	go func() {
		defer wg.Done()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/same", nil)
		handler.ServeHTTP(w, r)
	}()

	<-started // wait for first request to reach backend

	// Second request with same fingerprint - should wait for first
	wg.Add(1)
	var w2 *httptest.ResponseRecorder
	go func() {
		defer wg.Done()
		w2 = httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/same", nil)
		handler.ServeHTTP(w2, r)
	}()

	// Give the second request time to enter the inflight wait
	time.Sleep(50 * time.Millisecond)

	// Release the backend
	close(proceed)
	wg.Wait()

	if callCount != 1 {
		t.Errorf("expected 1 backend call (concurrent dedup), got %d", callCount)
	}
	if w2.Header().Get("X-Dedup-Replayed") != "true" {
		t.Error("second concurrent request should have X-Dedup-Replayed=true")
	}
}

func TestMiddlewareWithBody(t *testing.T) {
	cd, _ := New("test", config.RequestDedupConfig{
		Enabled: true,
		TTL:     5 * time.Second,
	}, nil)
	defer cd.Close()

	callCount := 0
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify body is still readable
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("backend failed to read body: %v", err)
		}
		callCount++
		w.WriteHeader(200)
		w.Write(body)
	})

	handler := cd.Middleware()(backend)

	// First request
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest("POST", "/test", bytes.NewReader([]byte("payload")))
	handler.ServeHTTP(w1, r1)

	if callCount != 1 {
		t.Errorf("expected 1 call, got %d", callCount)
	}
	if w1.Body.String() != "payload" {
		t.Errorf("unexpected body: %s", w1.Body.String())
	}

	// Same request - should be deduped
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest("POST", "/test", bytes.NewReader([]byte("payload")))
	handler.ServeHTTP(w2, r2)

	if callCount != 1 {
		t.Errorf("expected 1 call (deduped), got %d", callCount)
	}
}

func TestDedupByRoute(t *testing.T) {
	m := NewDedupByRoute()

	err := m.AddRoute("route-1", config.RequestDedupConfig{
		Enabled: true,
		TTL:     time.Second,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if h := m.GetHandler("route-1"); h == nil {
		t.Error("expected handler for route-1")
	}
	if h := m.GetHandler("route-2"); h != nil {
		t.Error("expected nil for non-existent route-2")
	}

	stats := m.Stats()
	if _, ok := stats["route-1"]; !ok {
		t.Error("expected stats for route-1")
	}
}

func TestDedupByRouteDisabled(t *testing.T) {
	m := NewDedupByRoute()
	err := m.AddRoute("route-1", config.RequestDedupConfig{
		Enabled: false,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h := m.GetHandler("route-1"); h != nil {
		t.Error("expected nil for disabled route")
	}
}

func TestMemoryStoreExpiry(t *testing.T) {
	store := NewMemoryStore(100 * time.Millisecond)
	defer store.Close()

	resp := &StoredResponse{StatusCode: 200, Body: []byte("test")}
	store.Set(nil, "key1", resp, 100*time.Millisecond)

	// Should exist
	got, _ := store.Get(nil, "key1")
	if got == nil {
		t.Fatal("expected stored response")
	}

	// Wait for expiry
	time.Sleep(150 * time.Millisecond)

	got, _ = store.Get(nil, "key1")
	if got != nil {
		t.Error("expected nil after expiry")
	}
}
