package quota

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/byroute"
)

func TestQuotaEnforcer_AllowsWithinLimit(t *testing.T) {
	cfg := config.QuotaConfig{
		Enabled: true,
		Limit:   5,
		Period:  "hourly",
		Key:     "ip",
	}
	qe := New("route1", cfg, nil)
	defer qe.Close()

	handler := qe.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if w.Code != 200 {
			t.Errorf("request %d: expected 200, got %d", i, w.Code)
		}
		if w.Header().Get("X-Quota-Limit") != "5" {
			t.Errorf("missing X-Quota-Limit header")
		}
	}

	if qe.allowed.Load() != 5 {
		t.Errorf("expected 5 allowed, got %d", qe.allowed.Load())
	}
}

func TestQuotaEnforcer_RejectsOverLimit(t *testing.T) {
	cfg := config.QuotaConfig{
		Enabled: true,
		Limit:   3,
		Period:  "daily",
		Key:     "ip",
	}
	qe := New("route1", cfg, nil)
	defer qe.Close()

	handler := qe.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if i < 3 {
			if w.Code != 200 {
				t.Errorf("request %d: expected 200, got %d", i, w.Code)
			}
		} else {
			if w.Code != 429 {
				t.Errorf("request %d: expected 429, got %d", i, w.Code)
			}
		}
	}

	if qe.rejected.Load() != 2 {
		t.Errorf("expected 2 rejected, got %d", qe.rejected.Load())
	}
}

func TestQuotaEnforcer_PerClientIsolation(t *testing.T) {
	cfg := config.QuotaConfig{
		Enabled: true,
		Limit:   2,
		Period:  "hourly",
		Key:     "ip",
	}
	qe := New("route1", cfg, nil)
	defer qe.Close()

	handler := qe.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Client A: 3 requests (2 allowed, 1 rejected)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.1.1.1:1234"
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
	}

	// Client B: should still have full quota
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "2.2.2.2:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Errorf("client B: expected 200, got %d", w.Code)
	}
}

func TestQuotaEnforcer_QuotaHeaders(t *testing.T) {
	cfg := config.QuotaConfig{
		Enabled: true,
		Limit:   10,
		Period:  "hourly",
		Key:     "ip",
	}
	qe := New("route1", cfg, nil)
	defer qe.Close()

	handler := qe.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Header().Get("X-Quota-Limit") != "10" {
		t.Errorf("expected X-Quota-Limit=10, got %s", w.Header().Get("X-Quota-Limit"))
	}
	if w.Header().Get("X-Quota-Remaining") != "9" {
		t.Errorf("expected X-Quota-Remaining=9, got %s", w.Header().Get("X-Quota-Remaining"))
	}
	if w.Header().Get("X-Quota-Reset") == "" {
		t.Error("expected X-Quota-Reset header")
	}
}

func TestQuotaEnforcer_RejectionHeaders(t *testing.T) {
	cfg := config.QuotaConfig{
		Enabled: true,
		Limit:   1,
		Period:  "monthly",
		Key:     "ip",
	}
	qe := New("route1", cfg, nil)
	defer qe.Close()

	handler := qe.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// First request: allowed
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Second request: rejected
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Errorf("expected 429, got %d", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header")
	}
	if w.Header().Get("X-Quota-Remaining") != "0" {
		t.Errorf("expected X-Quota-Remaining=0, got %s", w.Header().Get("X-Quota-Remaining"))
	}
}

func TestCurrentWindow_Hourly(t *testing.T) {
	qe := &QuotaEnforcer{period: "hourly"}
	now := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	start, end := qe.currentWindow(now)

	expected := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)
	if !start.Equal(expected) {
		t.Errorf("expected start %v, got %v", expected, start)
	}
	if !end.Equal(expected.Add(time.Hour)) {
		t.Errorf("expected end %v, got %v", expected.Add(time.Hour), end)
	}
}

func TestCurrentWindow_Daily(t *testing.T) {
	qe := &QuotaEnforcer{period: "daily"}
	now := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	start, end := qe.currentWindow(now)

	expected := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	if !start.Equal(expected) {
		t.Errorf("expected start %v, got %v", expected, start)
	}
	if !end.Equal(expected.AddDate(0, 0, 1)) {
		t.Errorf("expected end %v, got %v", expected.AddDate(0, 0, 1), end)
	}
}

func TestCurrentWindow_Monthly(t *testing.T) {
	qe := &QuotaEnforcer{period: "monthly"}
	now := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	start, end := qe.currentWindow(now)

	expected := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	if !start.Equal(expected) {
		t.Errorf("expected start %v, got %v", expected, start)
	}
	expectedEnd := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)
	if !end.Equal(expectedEnd) {
		t.Errorf("expected end %v, got %v", expectedEnd, end)
	}
}

func TestCurrentWindow_Yearly(t *testing.T) {
	qe := &QuotaEnforcer{period: "yearly"}
	now := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)
	start, end := qe.currentWindow(now)

	expected := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	if !start.Equal(expected) {
		t.Errorf("expected start %v, got %v", expected, start)
	}
	expectedEnd := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if !end.Equal(expectedEnd) {
		t.Errorf("expected end %v, got %v", expectedEnd, end)
	}

	// Verify Jan 1 boundary
	jan1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	start2, end2 := qe.currentWindow(jan1)
	if !start2.Equal(jan1) {
		t.Errorf("Jan 1 boundary: expected start %v, got %v", jan1, start2)
	}
	expectedEnd2 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !end2.Equal(expectedEnd2) {
		t.Errorf("Jan 1 boundary: expected end %v, got %v", expectedEnd2, end2)
	}
}

func TestQuotaByRoute(t *testing.T) {
	m := NewQuotaByRoute(nil)

	cfg := config.QuotaConfig{
		Enabled: true,
		Limit:   100,
		Period:  "daily",
		Key:     "ip",
	}
	m.AddRoute("route1", cfg)

	if qe := m.Lookup("route1"); qe == nil {
		t.Error("expected enforcer for route1")
	}
	if qe := m.Lookup("route2"); qe != nil {
		t.Error("expected nil for route2")
	}

	ids := m.RouteIDs()
	if len(ids) != 1 || ids[0] != "route1" {
		t.Errorf("unexpected route IDs: %v", ids)
	}

	stats := byroute.CollectStats(&m.Manager, func(qe *QuotaEnforcer) map[string]interface{} { return qe.Stats() })
	if len(stats) != 1 {
		t.Errorf("unexpected stats length: %d", len(stats))
	}

	byroute.ForEach(&m.Manager, (*QuotaEnforcer).Close)
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key   string
		valid bool
	}{
		{"ip", true},
		{"client_id", true},
		{"header:X-API-Key", true},
		{"jwt_claim:sub", true},
		{"header:", false},
		{"jwt_claim:", false},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		if got := ValidateKey(tt.key); got != tt.valid {
			t.Errorf("ValidateKey(%q) = %v, want %v", tt.key, got, tt.valid)
		}
	}
}

func TestQuotaEnforcer_Stats(t *testing.T) {
	cfg := config.QuotaConfig{
		Enabled: true,
		Limit:   100,
		Period:  "daily",
		Key:     "ip",
	}
	qe := New("route1", cfg, nil)
	defer qe.Close()

	stats := qe.Stats()
	if stats["limit"] != int64(100) {
		t.Errorf("expected limit=100, got %v", stats["limit"])
	}
	if stats["period"] != "daily" {
		t.Errorf("expected period=daily, got %v", stats["period"])
	}
	if stats["redis"] != false {
		t.Errorf("expected redis=false, got %v", stats["redis"])
	}
}
