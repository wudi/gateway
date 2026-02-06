package loadbalancer

import (
	"testing"
	"time"
)

func TestLeastResponseTimeColdStartPreference(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	lrt := NewLeastResponseTime([]*Backend{b1, b2})

	// Record latency for b1 only, b2 stays cold
	lrt.RecordLatency("http://a:8080", 10*time.Millisecond)

	got := lrt.Next()
	if got == nil || got.URL != "http://b:8080" {
		t.Fatalf("expected cold-start b, got %v", got)
	}
}

func TestLeastResponseTimeEWMAConvergence(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	lrt := NewLeastResponseTime([]*Backend{b1, b2})

	// Warm both backends
	lrt.RecordLatency("http://a:8080", 100*time.Millisecond)
	lrt.RecordLatency("http://b:8080", 10*time.Millisecond)

	got := lrt.Next()
	if got == nil || got.URL != "http://b:8080" {
		t.Fatalf("expected b (lower latency), got %v", got)
	}

	// Add more samples to converge
	for i := 0; i < 10; i++ {
		lrt.RecordLatency("http://a:8080", 100*time.Millisecond)
		lrt.RecordLatency("http://b:8080", 10*time.Millisecond)
	}

	got = lrt.Next()
	if got == nil || got.URL != "http://b:8080" {
		t.Fatalf("expected b after convergence, got %v", got)
	}
}

func TestLeastResponseTimeAllCold(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	lrt := NewLeastResponseTime([]*Backend{b1, b2})

	// Both cold — should return first in slice
	got := lrt.Next()
	if got == nil || got.URL != "http://a:8080" {
		t.Fatalf("expected a (first cold), got %v", got)
	}
}

func TestLeastResponseTimeAllUnhealthy(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: false}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: false}
	lrt := NewLeastResponseTime([]*Backend{b1, b2})

	if got := lrt.Next(); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestLeastResponseTimeUpdateBackends(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	lrt := NewLeastResponseTime([]*Backend{b1, b2})

	lrt.RecordLatency("http://a:8080", 10*time.Millisecond)
	lrt.RecordLatency("http://b:8080", 20*time.Millisecond)

	// Add a new backend, remove b
	b3 := &Backend{URL: "http://c:8080", Weight: 1, Healthy: true}
	lrt.UpdateBackends([]*Backend{b1, b3})

	lats := lrt.GetLatencies()

	// a should be preserved
	if _, ok := lats["http://a:8080"]; !ok {
		t.Fatal("expected a latency to be preserved")
	}
	// c should be added (cold)
	if _, ok := lats["http://c:8080"]; !ok {
		t.Fatal("expected c latency tracker to be added")
	}
	// b should be removed
	if _, ok := lats["http://b:8080"]; ok {
		t.Fatal("expected b latency tracker to be removed")
	}
}

func TestLeastResponseTimeGetLatencies(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	lrt := NewLeastResponseTime([]*Backend{b1})

	lrt.RecordLatency("http://a:8080", 50*time.Millisecond)

	lats := lrt.GetLatencies()
	if lat, ok := lats["http://a:8080"]; !ok || lat < 1 {
		t.Fatalf("expected recorded latency, got %v", lats)
	}
}

func TestLeastResponseTimeSkipsUnhealthy(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: false}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	lrt := NewLeastResponseTime([]*Backend{b1, b2})

	lrt.RecordLatency("http://b:8080", 100*time.Millisecond)

	got := lrt.Next()
	if got == nil || got.URL != "http://b:8080" {
		t.Fatalf("expected b (only healthy), got %v", got)
	}
}
