package costtrack

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestDefaultCostApplied(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-Cost") != "1" {
		t.Errorf("expected X-Request-Cost=1, got %s", rec.Header().Get("X-Request-Cost"))
	}

	if ct.totalCost.Load() != 1 {
		t.Errorf("expected total cost 1, got %d", ct.totalCost.Load())
	}
}

func TestCustomDefaultCost(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    5,
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-Cost") != "5" {
		t.Errorf("expected X-Request-Cost=5, got %s", rec.Header().Get("X-Request-Cost"))
	}

	if ct.totalCost.Load() != 5 {
		t.Errorf("expected total cost 5, got %d", ct.totalCost.Load())
	}
}

func TestMethodSpecificCost(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    1,
		CostByMethod: map[string]int{
			"POST":   5,
			"DELETE": 10,
		},
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	tests := []struct {
		method       string
		expectedCost string
	}{
		{"GET", "1"},
		{"POST", "5"},
		{"DELETE", "10"},
		{"PUT", "1"},
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, "/", nil)
		handler.ServeHTTP(rec, req)

		got := rec.Header().Get("X-Request-Cost")
		if got != tt.expectedCost {
			t.Errorf("method %s: expected X-Request-Cost=%s, got %s", tt.method, tt.expectedCost, got)
		}
	}

	// Total should be 1 + 5 + 10 + 1 = 17
	if ct.totalCost.Load() != 17 {
		t.Errorf("expected total cost 17, got %d", ct.totalCost.Load())
	}
}

func TestBudgetReject(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    3,
		Budget: &config.CostBudget{
			Limit:  5,
			Window: "hour",
			Action: "reject",
		},
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// First request: cost=3, total=3 <= 5 -> allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("first request: expected 200, got %d", rec.Code)
	}

	// Second request: cost=3, total=6 > 5 -> rejected
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", rec2.Code)
	}

	if rec2.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on rejected request")
	}

	if ct.totalRejected.Load() != 1 {
		t.Errorf("expected 1 rejected, got %d", ct.totalRejected.Load())
	}
}

func TestBudgetLogOnly(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    3,
		Budget: &config.CostBudget{
			Limit:  5,
			Window: "hour",
			Action: "log_only",
		},
	})

	backendCalled := 0
	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalled++
		w.WriteHeader(200)
	}))

	// First request: cost=3, total=3 <= 5 -> allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("first request: expected 200, got %d", rec.Code)
	}

	// Second request: cost=3, total=6 > 5 -> over budget but log_only allows it
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != 200 {
		t.Errorf("second request: expected 200 (log_only), got %d", rec2.Code)
	}

	if backendCalled != 2 {
		t.Errorf("expected backend called 2 times, got %d", backendCalled)
	}

	if ct.totalRejected.Load() != 1 {
		t.Errorf("expected 1 rejected (tracked even in log_only), got %d", ct.totalRejected.Load())
	}
}

func TestXRequestCostHeader(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    7,
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	costHeader := rec.Header().Get("X-Request-Cost")
	if costHeader != "7" {
		t.Errorf("expected X-Request-Cost=7, got %s", costHeader)
	}

	val, err := strconv.Atoi(costHeader)
	if err != nil {
		t.Fatalf("X-Request-Cost is not a valid integer: %v", err)
	}
	if val != 7 {
		t.Errorf("expected cost value 7, got %d", val)
	}
}

func TestStatsTracking(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    2,
		CostByMethod: map[string]int{
			"POST": 5,
		},
		Budget: &config.CostBudget{
			Limit:  100,
			Window: "hour",
			Action: "reject",
		},
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Send 3 GETs (cost 2 each) + 1 POST (cost 5)
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		handler.ServeHTTP(rec, req)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	handler.ServeHTTP(rec, req)

	stats := ct.Stats()
	if stats["default_cost"] != 2 {
		t.Errorf("expected default_cost=2, got %v", stats["default_cost"])
	}
	if stats["total_cost"].(int64) != 11 {
		t.Errorf("expected total_cost=11, got %v", stats["total_cost"])
	}
	if stats["total_requests"].(int64) != 4 {
		t.Errorf("expected total_requests=4, got %v", stats["total_requests"])
	}
	if stats["total_rejected"].(int64) != 0 {
		t.Errorf("expected total_rejected=0, got %v", stats["total_rejected"])
	}
	if stats["budget_limit"].(int64) != 100 {
		t.Errorf("expected budget_limit=100, got %v", stats["budget_limit"])
	}
	if stats["budget_action"] != "reject" {
		t.Errorf("expected budget_action=reject, got %v", stats["budget_action"])
	}
}

func TestCostByRouteManager(t *testing.T) {
	m := NewCostByRoute()

	m.AddRoute("route1", config.RequestCostConfig{
		Enabled: true,
		Cost:    3,
	})
	m.AddRoute("route2", config.RequestCostConfig{
		Enabled: true,
		Cost:    10,
		CostByMethod: map[string]int{
			"POST": 20,
		},
	})

	// GetTracker
	if m.GetTracker("route1") == nil {
		t.Error("expected tracker for route1")
	}
	if m.GetTracker("route2") == nil {
		t.Error("expected tracker for route2")
	}
	if m.GetTracker("nonexistent") != nil {
		t.Error("expected nil for nonexistent route")
	}

	// RouteIDs
	ids := m.RouteIDs()
	if len(ids) != 2 {
		t.Errorf("expected 2 route IDs, got %d", len(ids))
	}

	// Stats
	stats := m.Stats()
	if _, ok := stats["route1"]; !ok {
		t.Error("expected stats for route1")
	}
	if _, ok := stats["route2"]; !ok {
		t.Error("expected stats for route2")
	}

	// Exercise route2 to verify method cost
	ct2 := m.GetTracker("route2")
	handler := ct2.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-Cost") != "20" {
		t.Errorf("expected X-Request-Cost=20, got %s", rec.Header().Get("X-Request-Cost"))
	}
}

func TestParseWindow(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hour", "1h0m0s"},
		{"day", "24h0m0s"},
		{"month", "720h0m0s"},
		{"5m", "5m0s"},
		{"30s", "30s"},
		{"invalid", "1h0m0s"}, // defaults to hour
	}

	for _, tt := range tests {
		d := parseWindow(tt.input)
		if d.String() != tt.expected {
			t.Errorf("parseWindow(%q) = %s, expected %s", tt.input, d.String(), tt.expected)
		}
	}
}

func TestValidateKey(t *testing.T) {
	tests := []struct {
		key   string
		valid bool
	}{
		{"", true},
		{"ip", true},
		{"client_id", true},
		{"header:X-Api-Key", true},
		{"header:", false},
		{"cookie:session", false}, // not supported for cost tracking
		{"invalid", false},
	}

	for _, tt := range tests {
		if ValidateKey(tt.key) != tt.valid {
			t.Errorf("ValidateKey(%q) = %v, expected %v", tt.key, !tt.valid, tt.valid)
		}
	}
}

func TestValidateAction(t *testing.T) {
	tests := []struct {
		action string
		valid  bool
	}{
		{"", true},
		{"reject", true},
		{"log_only", true},
		{"block", false},
	}

	for _, tt := range tests {
		if ValidateAction(tt.action) != tt.valid {
			t.Errorf("ValidateAction(%q) = %v, expected %v", tt.action, !tt.valid, tt.valid)
		}
	}
}

func TestValidateWindow(t *testing.T) {
	tests := []struct {
		window string
		valid  bool
	}{
		{"hour", true},
		{"day", true},
		{"month", true},
		{"5m", true},
		{"invalid", false},
	}

	for _, tt := range tests {
		if ValidateWindow(tt.window) != tt.valid {
			t.Errorf("ValidateWindow(%q) = %v, expected %v", tt.window, !tt.valid, tt.valid)
		}
	}
}

func TestBudgetDefaultAction(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    10,
		Budget: &config.CostBudget{
			Limit:  5,
			Window: "hour",
			// Action defaults to "reject"
		},
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Exceeds budget on first request (cost 10 > limit 5)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 with default reject action, got %d", rec.Code)
	}
}

func TestDifferentConsumersHaveSeparateBudgets(t *testing.T) {
	ct := New(config.RequestCostConfig{
		Enabled: true,
		Cost:    3,
		Budget: &config.CostBudget{
			Limit:  5,
			Window: "hour",
			Action: "reject",
		},
	})

	handler := ct.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// Consumer A: first request (cost 3 <= 5) -> allowed
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("consumer A first request: expected 200, got %d", rec.Code)
	}

	// Consumer A: second request (cost 6 > 5) -> rejected
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("consumer A second request: expected 429, got %d", rec2.Code)
	}

	// Consumer B (different IP): first request -> should be allowed
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "5.6.7.8:5678"
	handler.ServeHTTP(rec3, req3)

	if rec3.Code != 200 {
		t.Errorf("consumer B first request: expected 200, got %d", rec3.Code)
	}
}
