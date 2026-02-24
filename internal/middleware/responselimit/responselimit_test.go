package responselimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wudi/gateway/config"
)

func TestRejectWithContentLength(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 100,
		Action:  "reject",
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := strings.Repeat("x", 200)
		w.Header().Set("Content-Length", "200")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusBadGateway {
		t.Errorf("expected 502, got %d", rr.Code)
	}
	if rr.Header().Get("X-Response-Limited") != "true" {
		t.Error("expected X-Response-Limited header")
	}
	if rr.Body.Len() != 0 {
		t.Errorf("expected empty body, got %d bytes", rr.Body.Len())
	}
}

func TestRejectStreamingExceedsLimit(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 50,
		Action:  "reject",
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Content-Length header — streaming
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strings.Repeat("a", 30)))
		w.Write([]byte(strings.Repeat("b", 30))) // exceeds 50 limit
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	// First 30 bytes written, second 30 discarded
	if rr.Body.Len() != 30 {
		t.Errorf("expected 30 bytes written, got %d", rr.Body.Len())
	}
	if rr.Header().Get("X-Response-Limited") != "true" {
		t.Error("expected X-Response-Limited header")
	}
}

func TestRejectUnderLimit(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 100,
		Action:  "reject",
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("small"))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if rr.Body.String() != "small" {
		t.Errorf("expected 'small', got %q", rr.Body.String())
	}
}

func TestTruncateExceedsLimit(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 10,
		Action:  "truncate",
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 50)))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Body.Len() != 10 {
		t.Errorf("expected 10 bytes, got %d", rr.Body.Len())
	}
}

func TestTruncateMultipleWrites(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 15,
		Action:  "truncate",
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("a", 10))) // 10 of 15 remaining
		w.Write([]byte(strings.Repeat("b", 10))) // only 5 written
		w.Write([]byte(strings.Repeat("c", 10))) // discarded
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Body.Len() != 15 {
		t.Errorf("expected 15 bytes, got %d", rr.Body.Len())
	}
	expected := strings.Repeat("a", 10) + strings.Repeat("b", 5)
	if rr.Body.String() != expected {
		t.Errorf("expected %q, got %q", expected, rr.Body.String())
	}
}

func TestLogOnlyExceedsLimit(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 10,
		Action:  "log_only",
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 50)))
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	// log_only should pass everything through
	if rr.Body.Len() != 50 {
		t.Errorf("expected 50 bytes (log_only), got %d", rr.Body.Len())
	}

	stats := rl.Stats()
	if stats.Limited != 1 {
		t.Errorf("expected limited=1, got %d", stats.Limited)
	}
}

func TestDefaultAction(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 100,
	})

	if rl.action != "reject" {
		t.Errorf("expected default action 'reject', got %q", rl.action)
	}
}

func TestMetrics(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 10,
		Action:  "reject",
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "20")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strings.Repeat("x", 20)))
	}))

	// Request 1: exceeds limit
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	// Request 2: under limit
	handler2 := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	rr2 := httptest.NewRecorder()
	handler2.ServeHTTP(rr2, httptest.NewRequest("GET", "/", nil))

	stats := rl.Stats()
	if stats.TotalResponses != 2 {
		t.Errorf("expected total_responses=2, got %d", stats.TotalResponses)
	}
	if stats.Limited != 1 {
		t.Errorf("expected limited=1, got %d", stats.Limited)
	}
	if stats.MaxSize != 10 {
		t.Errorf("expected max_size=10, got %d", stats.MaxSize)
	}
}

func TestIsEnabled(t *testing.T) {
	rl1 := New(config.ResponseLimitConfig{MaxSize: 100})
	if !rl1.IsEnabled() {
		t.Error("expected enabled with MaxSize=100")
	}

	rl2 := New(config.ResponseLimitConfig{MaxSize: 0})
	if rl2.IsEnabled() {
		t.Error("expected disabled with MaxSize=0")
	}
}

func TestFlush(t *testing.T) {
	rl := New(config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 1000,
	})

	handler := rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))

	if rr.Body.String() != "hello" {
		t.Errorf("expected 'hello', got %q", rr.Body.String())
	}
}

func TestResponseLimitByRoute(t *testing.T) {
	mgr := NewResponseLimitByRoute()

	mgr.AddRoute("api", config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 1 << 20, // 1MB
		Action:  "reject",
	})
	mgr.AddRoute("upload", config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 10 << 20, // 10MB
		Action:  "truncate",
	})

	if rl := mgr.GetLimiter("api"); rl == nil {
		t.Error("expected non-nil limiter for 'api'")
	}
	if rl := mgr.GetLimiter("missing"); rl != nil {
		t.Error("expected nil for missing route")
	}

	ids := mgr.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	stats := mgr.Stats()
	if len(stats) != 2 {
		t.Errorf("expected 2 stats entries, got %d", len(stats))
	}
	if stats["api"].MaxSize != 1<<20 {
		t.Errorf("expected api max_size=1MB, got %d", stats["api"].MaxSize)
	}
	if stats["upload"].Action != "truncate" {
		t.Errorf("expected upload action=truncate, got %q", stats["upload"].Action)
	}
}

func TestMergeResponseLimitConfig(t *testing.T) {
	global := config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 1 << 20,
		Action:  "reject",
	}

	// Per-route overrides max size
	perRoute := config.ResponseLimitConfig{
		Enabled: true,
		MaxSize: 5 << 20,
	}
	merged := MergeResponseLimitConfig(perRoute, global)
	if merged.MaxSize != 5<<20 {
		t.Errorf("expected max_size=5MB, got %d", merged.MaxSize)
	}
	if merged.Action != "reject" {
		t.Errorf("expected action from global, got %q", merged.Action)
	}

	// Per-route overrides action
	perRoute2 := config.ResponseLimitConfig{
		Enabled: true,
		Action:  "truncate",
	}
	merged2 := MergeResponseLimitConfig(perRoute2, global)
	if merged2.Action != "truncate" {
		t.Errorf("expected action=truncate, got %q", merged2.Action)
	}
	if merged2.MaxSize != 1<<20 {
		t.Errorf("expected max_size from global, got %d", merged2.MaxSize)
	}
}
