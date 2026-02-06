package loadbalancer

import (
	"sync"
	"testing"
)

func TestLeastConnectionsSingleBackend(t *testing.T) {
	backends := []*Backend{{URL: "http://a:8080", Weight: 1, Healthy: true}}
	lc := NewLeastConnections(backends)

	b := lc.Next()
	if b == nil || b.URL != "http://a:8080" {
		t.Fatalf("expected backend a, got %v", b)
	}
}

func TestLeastConnectionsPicksLeast(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	b3 := &Backend{URL: "http://c:8080", Weight: 1, Healthy: true}

	// Simulate active requests
	b1.IncrActive() // 1
	b1.IncrActive() // 2
	b2.IncrActive() // 1

	lc := NewLeastConnections([]*Backend{b1, b2, b3})

	got := lc.Next()
	if got == nil || got.URL != "http://c:8080" {
		t.Fatalf("expected c (0 active), got %v", got)
	}
}

func TestLeastConnectionsAllUnhealthy(t *testing.T) {
	backends := []*Backend{
		{URL: "http://a:8080", Weight: 1, Healthy: false},
		{URL: "http://b:8080", Weight: 1, Healthy: false},
	}
	lc := NewLeastConnections(backends)

	if b := lc.Next(); b != nil {
		t.Fatalf("expected nil, got %v", b)
	}
}

func TestLeastConnectionsSkipsUnhealthy(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: false}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	b2.IncrActive()
	b2.IncrActive()

	lc := NewLeastConnections([]*Backend{b1, b2})

	got := lc.Next()
	if got == nil || got.URL != "http://b:8080" {
		t.Fatalf("expected b (only healthy), got %v", got)
	}
}

func TestLeastConnectionsConcurrent(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	lc := NewLeastConnections([]*Backend{b1, b2})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := lc.Next()
			if b != nil {
				b.IncrActive()
				b.DecrActive()
			}
		}()
	}
	wg.Wait()

	if b1.GetActive() != 0 || b2.GetActive() != 0 {
		t.Fatalf("expected 0 active on both, got a=%d b=%d", b1.GetActive(), b2.GetActive())
	}
}

func TestLeastConnectionsTieBreaksToFirstInSlice(t *testing.T) {
	b1 := &Backend{URL: "http://a:8080", Weight: 1, Healthy: true}
	b2 := &Backend{URL: "http://b:8080", Weight: 1, Healthy: true}
	lc := NewLeastConnections([]*Backend{b1, b2})

	got := lc.Next()
	if got == nil || got.URL != "http://a:8080" {
		t.Fatalf("expected a (first in slice on tie), got %v", got)
	}
}
