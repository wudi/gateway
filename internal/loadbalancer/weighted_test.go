package loadbalancer

import (
	"testing"

	"github.com/wudi/gateway/internal/config"
)

func TestWeightedBalancerDistribution(t *testing.T) {
	splits := []config.TrafficSplitConfig{
		{
			Name:   "stable",
			Weight: 90,
			Backends: []config.BackendConfig{
				{URL: "http://stable:8080", Weight: 1},
			},
		},
		{
			Name:   "canary",
			Weight: 10,
			Backends: []config.BackendConfig{
				{URL: "http://canary:8080", Weight: 1},
			},
		},
	}

	wb := NewWeightedBalancer(splits)

	counts := map[string]int{}
	iterations := 10000

	for i := 0; i < iterations; i++ {
		b := wb.Next()
		if b == nil {
			t.Fatal("got nil backend")
		}
		counts[b.URL]++
	}

	// Check rough distribution (within 5% tolerance)
	stableRatio := float64(counts["http://stable:8080"]) / float64(iterations)
	canaryRatio := float64(counts["http://canary:8080"]) / float64(iterations)

	if stableRatio < 0.82 || stableRatio > 0.98 {
		t.Errorf("stable ratio %.2f out of expected range [0.82, 0.98]", stableRatio)
	}
	if canaryRatio < 0.02 || canaryRatio > 0.18 {
		t.Errorf("canary ratio %.2f out of expected range [0.02, 0.18]", canaryRatio)
	}
}

func TestWeightedBalancerHeaderOverride(t *testing.T) {
	splits := []config.TrafficSplitConfig{
		{
			Name:   "stable",
			Weight: 90,
			Backends: []config.BackendConfig{
				{URL: "http://stable:8080", Weight: 1},
			},
		},
		{
			Name:   "canary",
			Weight: 10,
			Backends: []config.BackendConfig{
				{URL: "http://canary:8080", Weight: 1},
			},
			MatchHeaders: map[string]string{"X-Canary": "true"},
		},
	}

	wb := NewWeightedBalancer(splits)

	// With canary header, should always go to canary
	headers := map[string]string{"X-Canary": "true"}
	for i := 0; i < 100; i++ {
		b := wb.NextForRequest(headers)
		if b == nil || b.URL != "http://canary:8080" {
			t.Fatal("expected canary backend with header override")
		}
	}
}

func TestWeightedBalancerInterface(t *testing.T) {
	splits := []config.TrafficSplitConfig{
		{
			Name:   "primary",
			Weight: 100,
			Backends: []config.BackendConfig{
				{URL: "http://primary:8080", Weight: 1},
			},
		},
	}

	wb := NewWeightedBalancer(splits)

	// Test Balancer interface methods
	backends := wb.GetBackends()
	if len(backends) != 1 {
		t.Errorf("expected 1 backend, got %d", len(backends))
	}

	if wb.HealthyCount() != 1 {
		t.Errorf("expected 1 healthy, got %d", wb.HealthyCount())
	}

	wb.MarkUnhealthy("http://primary:8080")
	if wb.HealthyCount() != 0 {
		t.Errorf("expected 0 healthy after mark unhealthy")
	}

	wb.MarkHealthy("http://primary:8080")
	if wb.HealthyCount() != 1 {
		t.Errorf("expected 1 healthy after mark healthy")
	}
}
