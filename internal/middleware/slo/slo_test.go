package slo

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestSlidingWindow_RecordAndSnapshot(t *testing.T) {
	w := NewSlidingWindow(time.Hour)

	for i := 0; i < 100; i++ {
		w.Record(i%10 == 0) // 10% errors
	}

	total, errors := w.Snapshot()
	if total != 100 {
		t.Fatalf("expected 100 total, got %d", total)
	}
	if errors != 10 {
		t.Fatalf("expected 10 errors, got %d", errors)
	}
}

func TestSlidingWindow_EmptySnapshot(t *testing.T) {
	w := NewSlidingWindow(time.Hour)
	total, errors := w.Snapshot()
	if total != 0 || errors != 0 {
		t.Fatalf("expected 0/0, got %d/%d", total, errors)
	}
}

func TestTracker_BudgetRemaining_AllGood(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled: true,
		Target:  0.999,
		Window:  time.Hour,
	})

	// Record 1000 successful requests
	for i := 0; i < 1000; i++ {
		tracker.window.Record(false)
	}

	budget := tracker.BudgetRemaining()
	if budget < 0.99 || budget > 1.0 {
		t.Fatalf("expected budget near 1.0, got %f", budget)
	}
}

func TestTracker_BudgetRemaining_Exhausted(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled: true,
		Target:  0.999,
		Window:  time.Hour,
	})

	// Record requests with high error rate (5%)
	for i := 0; i < 1000; i++ {
		tracker.window.Record(i%20 == 0) // 5% errors
	}

	budget := tracker.BudgetRemaining()
	if budget > 0 {
		t.Fatalf("expected budget <= 0 (exhausted), got %f", budget)
	}
}

func TestTracker_BudgetRemaining_Empty(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled: true,
		Target:  0.999,
		Window:  time.Hour,
	})

	budget := tracker.BudgetRemaining()
	if budget != 1.0 {
		t.Fatalf("expected 1.0 for empty window, got %f", budget)
	}
}

func TestTracker_Middleware_Normal(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled: true,
		Target:  0.999,
		Window:  time.Hour,
		Actions: []string{"add_header"},
	})

	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	})

	handler := tracker.Middleware()(backend)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	budgetHeader := rec.Header().Get("X-SLO-Budget-Remaining")
	if budgetHeader == "" {
		t.Fatal("expected X-SLO-Budget-Remaining header")
	}
}

func TestTracker_Middleware_RecordsErrors(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled:    true,
		Target:     0.999,
		Window:     time.Hour,
		ErrorCodes: []int{500, 502, 503},
	})

	handler := tracker.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
	}

	snap := tracker.Snapshot()
	if snap["errors"].(int64) != 10 {
		t.Fatalf("expected 10 errors, got %v", snap["errors"])
	}
}

func TestTracker_Middleware_LoadShedding(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled:         true,
		Target:          0.999,
		Window:          time.Hour,
		Actions:         []string{"shed_load"},
		ShedLoadPercent: 100, // 100% shed when budget exhausted
	})

	// Exhaust budget by recording lots of errors
	for i := 0; i < 1000; i++ {
		tracker.window.Record(true)
	}

	handler := tracker.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 503 {
		t.Fatalf("expected 503 load shed, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("expected Retry-After header")
	}
}

func TestTracker_Middleware_NoShedWhenBudgetOK(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled:         true,
		Target:          0.999,
		Window:          time.Hour,
		Actions:         []string{"shed_load"},
		ShedLoadPercent: 100,
	})

	// All successful requests
	for i := 0; i < 100; i++ {
		tracker.window.Record(false)
	}

	handler := tracker.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestTracker_DefaultErrorCodes(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled: true,
		Target:  0.999,
		Window:  time.Hour,
	})

	// 500-599 should be errors by default
	handler := tracker.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	snap := tracker.Snapshot()
	if snap["errors"].(int64) != 1 {
		t.Fatalf("expected 1 error, got %v", snap["errors"])
	}

	// 400 should NOT be an error
	handler2 := tracker.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
	}))
	req2 := httptest.NewRequest("GET", "/", nil)
	rec2 := httptest.NewRecorder()
	handler2.ServeHTTP(rec2, req2)

	snap2 := tracker.Snapshot()
	if snap2["errors"].(int64) != 1 {
		t.Fatalf("expected still 1 error, got %v", snap2["errors"])
	}
}

func TestTracker_DefaultShedLoadPercent(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled: true,
		Target:  0.999,
		Window:  time.Hour,
		Actions: []string{"shed_load"},
		// ShedLoadPercent is 0, should default to 10
	})
	if tracker.shedLoadPercent != 10 {
		t.Fatalf("expected default shed_load_percent of 10, got %f", tracker.shedLoadPercent)
	}
}

func TestTracker_Snapshot(t *testing.T) {
	tracker := NewTracker(config.SLOConfig{
		Enabled: true,
		Target:  0.99,
		Window:  time.Hour,
	})

	for i := 0; i < 50; i++ {
		tracker.window.Record(i%10 == 0)
	}

	snap := tracker.Snapshot()
	if snap["target"].(float64) != 0.99 {
		t.Fatalf("expected target 0.99, got %v", snap["target"])
	}
	if snap["total"].(int64) != 50 {
		t.Fatalf("expected 50 total, got %v", snap["total"])
	}
	if snap["errors"].(int64) != 5 {
		t.Fatalf("expected 5 errors, got %v", snap["errors"])
	}
}

func TestSLOByRoute(t *testing.T) {
	m := NewSLOByRoute()
	m.AddRoute("route1", config.SLOConfig{
		Enabled: true,
		Target:  0.999,
		Window:  time.Hour,
	})

	if m.Lookup("route1") == nil {
		t.Fatal("expected tracker")
	}
	if m.Lookup("nonexistent") != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	stats := m.Stats()
	if stats["route1"] == nil {
		t.Fatal("expected stats for route1")
	}
}

func TestSloWriter_Flush(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sloWriter{ResponseWriter: rec, statusCode: 200}
	sw.Flush() // should not panic
}

func TestSloWriter_WriteHeader_OnlyOnce(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sloWriter{ResponseWriter: rec, statusCode: 200}
	sw.WriteHeader(404)
	sw.WriteHeader(500) // should be ignored
	if sw.statusCode != 404 {
		t.Fatalf("expected 404, got %d", sw.statusCode)
	}
}
