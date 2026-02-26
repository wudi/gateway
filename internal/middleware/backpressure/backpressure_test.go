package backpressure

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/loadbalancer"
	"github.com/wudi/runway/variables"
)

// mockBalancer implements loadbalancer.Balancer for testing.
type mockBalancer struct {
	mu        sync.Mutex
	backends  []*loadbalancer.Backend
	unhealthy map[string]bool
	healthy   map[string]bool
}

func newMockBalancer(urls ...string) *mockBalancer {
	mb := &mockBalancer{
		unhealthy: make(map[string]bool),
		healthy:   make(map[string]bool),
	}
	for _, u := range urls {
		mb.backends = append(mb.backends, &loadbalancer.Backend{URL: u, Healthy: true})
	}
	return mb
}

func (m *mockBalancer) Next() *loadbalancer.Backend {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range m.backends {
		if b.Healthy {
			return b
		}
	}
	return nil
}

func (m *mockBalancer) UpdateBackends(backends []*loadbalancer.Backend) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backends = backends
}

func (m *mockBalancer) MarkHealthy(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthy[url] = true
	for _, b := range m.backends {
		if b.URL == url {
			b.Healthy = true
		}
	}
}

func (m *mockBalancer) MarkUnhealthy(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unhealthy[url] = true
	for _, b := range m.backends {
		if b.URL == url {
			b.Healthy = false
		}
	}
}

func (m *mockBalancer) GetBackends() []*loadbalancer.Backend {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*loadbalancer.Backend, len(m.backends))
	copy(result, m.backends)
	return result
}

func (m *mockBalancer) HealthyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, b := range m.backends {
		if b.Healthy {
			count++
		}
	}
	return count
}

func (m *mockBalancer) wasMarkedUnhealthy(url string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unhealthy[url]
}

func (m *mockBalancer) wasMarkedHealthy(url string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.healthy[url]
}

// withVarContext attaches a variables.Context with the given upstream address to the request.
func withVarContext(r *http.Request, upstreamAddr string) *http.Request {
	vc := variables.AcquireContext(r)
	vc.UpstreamAddr = upstreamAddr
	ctx := context.WithValue(r.Context(), variables.RequestContextKey{}, vc)
	return r.WithContext(ctx)
}

func TestBackpressure_Detects429(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{}, bal)
	defer bp.Close()

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))

	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if w.Code != 429 {
		t.Errorf("expected status 429 to pass through, got %d", w.Code)
	}
	if !bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("expected backend to be marked unhealthy on 429")
	}
	if bp.throttled.Load() != 1 {
		t.Errorf("expected throttled=1, got %d", bp.throttled.Load())
	}
}

func TestBackpressure_Detects503(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{}, bal)
	defer bp.Close()

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))

	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if !bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("expected backend to be marked unhealthy on 503")
	}
}

func TestBackpressure_IgnoresNonThrottleStatus(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{}, bal)
	defer bp.Close()

	for _, code := range []int{200, 201, 301, 400, 404, 500, 502} {
		handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))

		w := httptest.NewRecorder()
		r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
		handler.ServeHTTP(w, r)
	}

	if bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("expected backend NOT to be marked unhealthy for non-throttle statuses")
	}
	if bp.throttled.Load() != 0 {
		t.Errorf("expected throttled=0, got %d", bp.throttled.Load())
	}
}

func TestBackpressure_CustomStatusCodes(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{
		StatusCodes: []int{502},
	}, bal)
	defer bp.Close()

	// 429 should NOT trigger with custom codes
	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("429 should not trigger with custom status codes [502]")
	}

	// 502 should trigger
	handler = bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
	}))
	w = httptest.NewRecorder()
	r = withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if !bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("502 should trigger with custom status codes [502]")
	}
}

func TestBackpressure_AutoRecovery(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{
		DefaultDelay: 50 * time.Millisecond,
	}, bal)
	defer bp.Close()

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))

	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if !bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Fatal("expected backend to be marked unhealthy")
	}

	// Wait for recovery timer to fire.
	time.Sleep(150 * time.Millisecond)

	if !bal.wasMarkedHealthy("http://backend1:8080") {
		t.Error("expected backend to be marked healthy after recovery delay")
	}
	if bp.recovered.Load() != 1 {
		t.Errorf("expected recovered=1, got %d", bp.recovered.Load())
	}
}

func TestBackpressure_RetryAfterSeconds(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{
		DefaultDelay:  5 * time.Second,
		MaxRetryAfter: 60 * time.Second,
	}, bal)
	defer bp.Close()

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(429)
	}))

	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if !bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("expected backend to be marked unhealthy")
	}
}

func TestBackpressure_RetryAfterHTTPDate(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{
		DefaultDelay:  50 * time.Millisecond,
		MaxRetryAfter: 60 * time.Second,
	}, bal)
	defer bp.Close()

	futureTime := time.Now().Add(80 * time.Millisecond).UTC().Format(http.TimeFormat)

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", futureTime)
		w.WriteHeader(429)
	}))

	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if !bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("expected backend to be marked unhealthy")
	}

	// Wait for recovery (the HTTP-date is ~80ms in the future).
	time.Sleep(200 * time.Millisecond)

	if !bal.wasMarkedHealthy("http://backend1:8080") {
		t.Error("expected backend to be marked healthy after HTTP-date delay")
	}
}

func TestBackpressure_RetryAfterMaxCap(t *testing.T) {
	bp := New(config.BackpressureConfig{
		MaxRetryAfter: 10 * time.Second,
	}, newMockBalancer())

	d := bp.parseRetryAfter("999")
	if d != 10*time.Second {
		t.Errorf("expected capped at 10s, got %v", d)
	}
}

func TestBackpressure_RetryAfterPastDate(t *testing.T) {
	bp := New(config.BackpressureConfig{
		DefaultDelay: 5 * time.Second,
	}, newMockBalancer())

	pastTime := time.Now().Add(-1 * time.Hour).UTC().Format(http.TimeFormat)
	d := bp.parseRetryAfter(pastTime)
	if d != 5*time.Second {
		t.Errorf("expected default delay for past date, got %v", d)
	}
}

func TestBackpressure_RetryAfterInvalidValue(t *testing.T) {
	bp := New(config.BackpressureConfig{
		DefaultDelay: 7 * time.Second,
	}, newMockBalancer())

	d := bp.parseRetryAfter("not-a-number-or-date")
	if d != 7*time.Second {
		t.Errorf("expected default delay for invalid value, got %v", d)
	}
}

func TestBackpressure_DefaultDelayWhenNoHeader(t *testing.T) {
	bp := New(config.BackpressureConfig{
		DefaultDelay: 3 * time.Second,
	}, newMockBalancer())

	d := bp.parseRetryAfter("")
	if d != 3*time.Second {
		t.Errorf("expected 3s default, got %v", d)
	}
}

func TestBackpressure_NoUpstreamAddr(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{}, bal)
	defer bp.Close()

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))

	// Request without variable context (no UpstreamAddr).
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("should not mark unhealthy when UpstreamAddr is empty")
	}
}

func TestBackpressure_TimerResetOnRepeatThrottle(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{
		DefaultDelay: 100 * time.Millisecond,
	}, bal)
	defer bp.Close()

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))

	// First 429
	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	// Wait a bit, then send another 429 (should reset the timer).
	time.Sleep(60 * time.Millisecond)

	w = httptest.NewRecorder()
	r = withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if bp.throttled.Load() != 2 {
		t.Errorf("expected throttled=2, got %d", bp.throttled.Load())
	}

	// After 60ms more (120ms total from second 429), recovery should NOT have fired yet
	// from the first timer (which would have been at 100ms from first request).
	// The second timer resets to 100ms from the second request.
	time.Sleep(60 * time.Millisecond)

	// Wait for the second timer to fire.
	time.Sleep(80 * time.Millisecond)

	if !bal.wasMarkedHealthy("http://backend1:8080") {
		t.Error("expected recovery after second timer")
	}
	// Only one recovery should have fired (first timer was cancelled).
	if bp.recovered.Load() != 1 {
		t.Errorf("expected recovered=1 (first timer cancelled), got %d", bp.recovered.Load())
	}
}

func TestBackpressure_Stats(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{
		DefaultDelay: 1 * time.Hour, // long delay so timer stays pending
	}, bal)
	defer bp.Close()

	stats := bp.Stats()
	if stats["throttled"].(int64) != 0 {
		t.Errorf("expected throttled=0, got %v", stats["throttled"])
	}
	if stats["recovered"].(int64) != 0 {
		t.Errorf("expected recovered=0, got %v", stats["recovered"])
	}
	if stats["pending"].(int) != 0 {
		t.Errorf("expected pending=0, got %v", stats["pending"])
	}

	// Trigger a 429
	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	stats = bp.Stats()
	if stats["throttled"].(int64) != 1 {
		t.Errorf("expected throttled=1, got %v", stats["throttled"])
	}
	if stats["pending"].(int) != 1 {
		t.Errorf("expected pending=1, got %v", stats["pending"])
	}
}

func TestBackpressure_Close(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{
		DefaultDelay: 1 * time.Hour,
	}, bal)

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))

	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	bp.Close()

	// After close, timer map should be empty.
	count := 0
	bp.timers.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	if count != 0 {
		t.Errorf("expected all timers cleared after Close, got %d", count)
	}
}

func TestBackpressureByRoute(t *testing.T) {
	m := NewBackpressureByRoute()
	bal := newMockBalancer("http://backend1:8080")
	m.AddRoute("route1", config.BackpressureConfig{}, bal)

	if h := m.GetHandler("route1"); h == nil {
		t.Fatal("expected handler for route1")
	}
	if h := m.GetHandler("nonexistent"); h != nil {
		t.Fatal("expected nil for nonexistent route")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("expected [route1], got %v", ids)
	}

	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}

	m.CloseAll()
}

func TestBackpressure_StatusCaptureWrite(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080")
	bp := New(config.BackpressureConfig{}, bal)
	defer bp.Close()

	// Test implicit 200 via Write (no explicit WriteHeader call).
	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))

	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Errorf("expected implicit 200, got %d", w.Code)
	}
	if bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("200 should not trigger backpressure")
	}
}

func TestBackpressure_MultipleBackends(t *testing.T) {
	bal := newMockBalancer("http://backend1:8080", "http://backend2:8080")
	bp := New(config.BackpressureConfig{
		DefaultDelay: 50 * time.Millisecond,
	}, bal)
	defer bp.Close()

	handler := bp.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))

	// Mark backend1 unhealthy
	w := httptest.NewRecorder()
	r := withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend1:8080")
	handler.ServeHTTP(w, r)

	// Mark backend2 unhealthy
	w = httptest.NewRecorder()
	r = withVarContext(httptest.NewRequest("GET", "/", nil), "http://backend2:8080")
	handler.ServeHTTP(w, r)

	if !bal.wasMarkedUnhealthy("http://backend1:8080") {
		t.Error("expected backend1 to be marked unhealthy")
	}
	if !bal.wasMarkedUnhealthy("http://backend2:8080") {
		t.Error("expected backend2 to be marked unhealthy")
	}
	if bp.throttled.Load() != 2 {
		t.Errorf("expected throttled=2, got %d", bp.throttled.Load())
	}

	stats := bp.Stats()
	if stats["pending"].(int) != 2 {
		t.Errorf("expected pending=2, got %v", stats["pending"])
	}

	// Wait for both to recover.
	time.Sleep(150 * time.Millisecond)

	if !bal.wasMarkedHealthy("http://backend1:8080") {
		t.Error("expected backend1 to recover")
	}
	if !bal.wasMarkedHealthy("http://backend2:8080") {
		t.Error("expected backend2 to recover")
	}
	if bp.recovered.Load() != 2 {
		t.Errorf("expected recovered=2, got %d", bp.recovered.Load())
	}
}

func TestBackpressure_DefaultsApplied(t *testing.T) {
	bp := New(config.BackpressureConfig{}, newMockBalancer())

	// Should default to 429 and 503.
	if !bp.statusSet[429] {
		t.Error("expected 429 in default status set")
	}
	if !bp.statusSet[503] {
		t.Error("expected 503 in default status set")
	}
	if bp.cfg.DefaultDelay != 5*time.Second {
		t.Errorf("expected default delay 5s, got %v", bp.cfg.DefaultDelay)
	}
	if bp.cfg.MaxRetryAfter != 60*time.Second {
		t.Errorf("expected max retry after 60s, got %v", bp.cfg.MaxRetryAfter)
	}
}
