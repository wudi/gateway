package statusmap

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusMapper_Remap(t *testing.T) {
	sm := New("test", map[int]int{
		404: 200,
		500: 503,
	})

	handler := sm.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected remapped status 200, got %d", w.Code)
	}
	if w.Body.String() != "not found" {
		t.Errorf("expected body 'not found', got %q", w.Body.String())
	}
	if sm.total.Load() != 1 {
		t.Errorf("expected total=1, got %d", sm.total.Load())
	}
	if sm.remapped.Load() != 1 {
		t.Errorf("expected remapped=1, got %d", sm.remapped.Load())
	}
}

func TestStatusMapper_Passthrough(t *testing.T) {
	sm := New("test", map[int]int{404: 200})

	handler := sm.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		w.Write([]byte("created"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 201 {
		t.Errorf("expected passthrough status 201, got %d", w.Code)
	}
	if sm.total.Load() != 1 {
		t.Errorf("expected total=1, got %d", sm.total.Load())
	}
	if sm.remapped.Load() != 0 {
		t.Errorf("expected remapped=0, got %d", sm.remapped.Load())
	}
}

func TestStatusMapper_ImplicitOK(t *testing.T) {
	sm := New("test", map[int]int{200: 202})

	handler := sm.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No explicit WriteHeader — implicit 200
		w.Write([]byte("ok"))
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 202 {
		t.Errorf("expected remapped implicit 200→202, got %d", w.Code)
	}
}

func TestStatusMapper_DoubleWriteHeader(t *testing.T) {
	sm := New("test", map[int]int{404: 200})

	handler := sm.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.WriteHeader(500) // should be ignored
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected first writeheader to win (200), got %d", w.Code)
	}
	if sm.total.Load() != 1 {
		t.Errorf("expected total=1 (second ignored), got %d", sm.total.Load())
	}
}

func TestStatusMapper_Flush(t *testing.T) {
	sm := New("test", map[int]int{})

	handler := sm.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("data"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if !w.Flushed {
		t.Error("expected Flush to be forwarded")
	}
}

func TestStatusMapper_Stats(t *testing.T) {
	sm := New("test", map[int]int{404: 200})
	stats := sm.Stats()
	if stats["total"].(int64) != 0 {
		t.Errorf("expected 0 total, got %v", stats["total"])
	}
	if stats["remapped"].(int64) != 0 {
		t.Errorf("expected 0 remapped, got %v", stats["remapped"])
	}
}

func TestStatusMapByRoute(t *testing.T) {
	m := NewStatusMapByRoute()
	m.AddRoute("r1", map[int]int{404: 200})

	if sm := m.Lookup("r1"); sm == nil {
		t.Fatal("expected mapper for r1")
	}
	if sm := m.Lookup("nonexistent"); sm != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "r1" {
		t.Errorf("expected [r1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["r1"]; !ok {
		t.Error("expected stats for r1")
	}
}
