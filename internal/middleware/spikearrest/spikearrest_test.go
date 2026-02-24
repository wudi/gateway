package spikearrest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestSpikeArrester_AllowsWithinRate(t *testing.T) {
	sa := New(config.SpikeArrestConfig{
		Enabled: true,
		Rate:    100,
		Period:  time.Second,
	})

	handler := sa.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 50; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", i, rr.Code)
		}
	}

	stats := sa.Stats()
	if stats["allowed"].(int64) != 50 {
		t.Errorf("expected 50 allowed, got %v", stats["allowed"])
	}
}

func TestSpikeArrester_RejectsOverBurst(t *testing.T) {
	sa := New(config.SpikeArrestConfig{
		Enabled: true,
		Rate:    5,
		Period:  time.Second,
		Burst:   5,
	})

	handler := sa.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	allowed := 0
	rejected := 0
	for i := 0; i < 20; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		handler.ServeHTTP(rr, req)
		if rr.Code == http.StatusOK {
			allowed++
		} else if rr.Code == http.StatusTooManyRequests {
			rejected++
		}
	}

	if allowed != 5 {
		t.Errorf("expected 5 allowed, got %d", allowed)
	}
	if rejected != 15 {
		t.Errorf("expected 15 rejected, got %d", rejected)
	}
}

func TestSpikeArrester_PerIP(t *testing.T) {
	sa := New(config.SpikeArrestConfig{
		Enabled: true,
		Rate:    2,
		Period:  time.Second,
		Burst:   2,
		PerIP:   true,
	})

	handler := sa.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// IP 1: 2 allowed, 1 rejected
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("IP1 request %d: expected 200, got %d", i, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:1234"
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("IP1 3rd request should be rejected, got %d", rr.Code)
	}

	// IP 2: should still have its own quota
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "5.6.7.8:1234"
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("IP2 request 1: expected 200, got %d", rr.Code)
	}

	stats := sa.Stats()
	if stats["per_ip"] != true {
		t.Error("expected per_ip=true in stats")
	}
	if stats["tracked_ips"].(int) != 2 {
		t.Errorf("expected 2 tracked IPs, got %v", stats["tracked_ips"])
	}
}

func TestMergeSpikeArrestConfig(t *testing.T) {
	global := config.SpikeArrestConfig{
		Enabled: true,
		Rate:    100,
		Period:  time.Second,
		Burst:   50,
	}
	perRoute := config.SpikeArrestConfig{
		Enabled: true,
		Rate:    50,
	}

	merged := MergeSpikeArrestConfig(perRoute, global)
	if merged.Rate != 50 {
		t.Errorf("expected merged rate=50, got %d", merged.Rate)
	}
	if merged.Period != time.Second {
		t.Errorf("expected merged period=1s, got %v", merged.Period)
	}
	if merged.Burst != 50 {
		t.Errorf("expected merged burst=50, got %d", merged.Burst)
	}
}

func TestSpikeArrestByRoute(t *testing.T) {
	m := NewSpikeArrestByRoute()

	m.AddRoute("route1", config.SpikeArrestConfig{
		Enabled: true,
		Rate:    10,
		Period:  time.Second,
	})

	if sa := m.GetArrester("route1"); sa == nil {
		t.Fatal("expected arrester for route1")
	}
	if sa := m.GetArrester("nonexistent"); sa != nil {
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
}
