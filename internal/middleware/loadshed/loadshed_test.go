package loadshed

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestLoadShedder_GoroutineLimit(t *testing.T) {
	cfg := config.LoadSheddingConfig{
		Enabled:          true,
		GoroutineLimit:   1, // very low, will trigger
		CPUThreshold:     100,
		MemoryThreshold:  100,
		SampleInterval:   10 * time.Millisecond,
		RetryAfter:       3,
		CooldownDuration: 50 * time.Millisecond,
	}

	ls := New(cfg)
	defer ls.Close()

	// Wait for at least one sample
	time.Sleep(50 * time.Millisecond)

	handler := ls.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "3" {
		t.Errorf("expected Retry-After: 3, got %s", rec.Header().Get("Retry-After"))
	}
}

func TestLoadShedder_NoShedding(t *testing.T) {
	cfg := config.LoadSheddingConfig{
		Enabled:         true,
		GoroutineLimit:  1000000, // very high, won't trigger
		MemoryThreshold: 100,     // impossible to exceed (check is >)
		CPUThreshold:    100,     // impossible to exceed (check is >)
		SampleInterval:  10 * time.Millisecond,
		CooldownDuration: 10 * time.Millisecond,
	}

	ls := New(cfg)
	defer ls.Close()

	// Wait for sample
	time.Sleep(50 * time.Millisecond)

	handler := ls.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestLoadShedder_Cooldown(t *testing.T) {
	cfg := config.LoadSheddingConfig{
		Enabled:          true,
		GoroutineLimit:   1, // will trigger immediately
		CPUThreshold:     100,
		MemoryThreshold:  100,
		SampleInterval:   10 * time.Millisecond,
		CooldownDuration: 100 * time.Millisecond,
	}

	ls := New(cfg)
	defer ls.Close()

	// Wait for at least one sample to trigger shedding
	time.Sleep(100 * time.Millisecond)

	if !ls.shedding.Load() {
		t.Fatal("expected shedding to be active")
	}

	// Raise the limit so goroutines no longer exceed the threshold.
	ls.cfg.GoroutineLimit = 1000000

	// Shedding should remain during cooldown
	time.Sleep(30 * time.Millisecond)
	if !ls.shedding.Load() {
		t.Fatal("expected shedding to still be active during cooldown")
	}

	// After cooldown expires + several sample intervals, shedding should clear.
	// Use generous margin because runtime.ReadMemStats() can stall the sample loop.
	time.Sleep(500 * time.Millisecond)
	if ls.shedding.Load() {
		t.Fatal("expected shedding to be cleared after cooldown")
	}
}

func TestLoadShedder_Stats(t *testing.T) {
	cfg := config.LoadSheddingConfig{
		Enabled:          true,
		GoroutineLimit:   1000000,
		CPUThreshold:     100,
		MemoryThreshold:  100,
		SampleInterval:   10 * time.Millisecond,
		CooldownDuration: 10 * time.Millisecond,
	}

	ls := New(cfg)
	defer ls.Close()

	time.Sleep(30 * time.Millisecond)

	stats := ls.Stats()
	if stats["enabled"] != true {
		t.Error("expected enabled=true")
	}
	if stats["shedding"] != false {
		t.Error("expected shedding=false")
	}
	if _, ok := stats["cpu_percent"]; !ok {
		t.Error("expected cpu_percent in stats")
	}
	if _, ok := stats["memory_percent"]; !ok {
		t.Error("expected memory_percent in stats")
	}
}
