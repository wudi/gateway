package loadbalancer

import (
	"testing"
)

func TestRoundRobin(t *testing.T) {
	backends := []*Backend{
		{URL: "http://server1:8080", Weight: 1, Healthy: true},
		{URL: "http://server2:8080", Weight: 1, Healthy: true},
		{URL: "http://server3:8080", Weight: 1, Healthy: true},
	}

	rr := NewRoundRobin(backends)

	// Should cycle through backends
	results := make(map[string]int)
	for i := 0; i < 9; i++ {
		b := rr.Next()
		results[b.URL]++
	}

	// Each backend should be hit exactly 3 times
	for _, b := range backends {
		if results[b.URL] != 3 {
			t.Errorf("expected backend %s to be hit 3 times, got %d", b.URL, results[b.URL])
		}
	}
}

func TestRoundRobinWithUnhealthy(t *testing.T) {
	backends := []*Backend{
		{URL: "http://server1:8080", Weight: 1, Healthy: true},
		{URL: "http://server2:8080", Weight: 1, Healthy: false},
		{URL: "http://server3:8080", Weight: 1, Healthy: true},
	}

	rr := NewRoundRobin(backends)

	// Should only return healthy backends
	results := make(map[string]int)
	for i := 0; i < 10; i++ {
		b := rr.Next()
		if b.URL == "http://server2:8080" {
			t.Error("should not return unhealthy backend")
		}
		results[b.URL]++
	}

	if results["http://server1:8080"] != 5 {
		t.Errorf("expected server1 to be hit 5 times, got %d", results["http://server1:8080"])
	}
	if results["http://server3:8080"] != 5 {
		t.Errorf("expected server3 to be hit 5 times, got %d", results["http://server3:8080"])
	}
}

func TestRoundRobinMarkUnhealthy(t *testing.T) {
	backends := []*Backend{
		{URL: "http://server1:8080", Weight: 1, Healthy: true},
		{URL: "http://server2:8080", Weight: 1, Healthy: true},
	}

	rr := NewRoundRobin(backends)

	// Mark server1 as unhealthy
	rr.MarkUnhealthy("http://server1:8080")

	// Should only return server2
	for i := 0; i < 5; i++ {
		b := rr.Next()
		if b.URL != "http://server2:8080" {
			t.Errorf("expected server2, got %s", b.URL)
		}
	}

	// Mark server1 as healthy again
	rr.MarkHealthy("http://server1:8080")

	// Should now return both
	results := make(map[string]int)
	for i := 0; i < 10; i++ {
		b := rr.Next()
		results[b.URL]++
	}

	if results["http://server1:8080"] == 0 {
		t.Error("server1 should be returned after marked healthy")
	}
}

func TestRoundRobinNoHealthy(t *testing.T) {
	backends := []*Backend{
		{URL: "http://server1:8080", Weight: 1, Healthy: false},
		{URL: "http://server2:8080", Weight: 1, Healthy: false},
	}

	rr := NewRoundRobin(backends)

	b := rr.Next()
	if b != nil {
		t.Error("should return nil when no healthy backends")
	}
}

func TestRoundRobinUpdateBackends(t *testing.T) {
	backends := []*Backend{
		{URL: "http://server1:8080", Weight: 1, Healthy: true},
	}

	rr := NewRoundRobin(backends)

	// Update with new backends
	newBackends := []*Backend{
		{URL: "http://server2:8080", Weight: 1, Healthy: true},
		{URL: "http://server3:8080", Weight: 1, Healthy: true},
	}

	rr.UpdateBackends(newBackends)

	// Should return new backends
	results := make(map[string]int)
	for i := 0; i < 10; i++ {
		b := rr.Next()
		results[b.URL]++
	}

	if results["http://server1:8080"] != 0 {
		t.Error("server1 should not be returned after update")
	}
	if results["http://server2:8080"] != 5 {
		t.Errorf("expected server2 5 times, got %d", results["http://server2:8080"])
	}
}

func TestHealthyCount(t *testing.T) {
	backends := []*Backend{
		{URL: "http://server1:8080", Weight: 1, Healthy: true},
		{URL: "http://server2:8080", Weight: 1, Healthy: false},
		{URL: "http://server3:8080", Weight: 1, Healthy: true},
	}

	rr := NewRoundRobin(backends)

	if rr.HealthyCount() != 2 {
		t.Errorf("expected healthy count 2, got %d", rr.HealthyCount())
	}

	rr.MarkUnhealthy("http://server1:8080")

	if rr.HealthyCount() != 1 {
		t.Errorf("expected healthy count 1, got %d", rr.HealthyCount())
	}
}

func TestWeightedRoundRobin(t *testing.T) {
	backends := []*Backend{
		{URL: "http://server1:8080", Weight: 3, Healthy: true},
		{URL: "http://server2:8080", Weight: 1, Healthy: true},
	}

	wrr := NewWeightedRoundRobin(backends)

	// With weights 3:1, server1 should be selected ~3x as often
	results := make(map[string]int)
	for i := 0; i < 100; i++ {
		b := wrr.Next()
		results[b.URL]++
	}

	ratio := float64(results["http://server1:8080"]) / float64(results["http://server2:8080"])
	// Allow some variance, but should be approximately 3:1
	if ratio < 2.0 || ratio > 4.0 {
		t.Errorf("expected ratio ~3:1, got %.2f (server1: %d, server2: %d)",
			ratio, results["http://server1:8080"], results["http://server2:8080"])
	}
}

func BenchmarkRoundRobinNext(b *testing.B) {
	backends := make([]*Backend, 10)
	for i := 0; i < 10; i++ {
		backends[i] = &Backend{
			URL:     "http://server" + string(rune('0'+i)) + ":8080",
			Weight:  1,
			Healthy: true,
		}
	}

	rr := NewRoundRobin(backends)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr.Next()
	}
}
