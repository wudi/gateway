package circuitbreaker

import (
	"fmt"
	"testing"
	"time"

	"github.com/example/gateway/internal/config"
)

func TestNewBreakerDefaults(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{}, nil)

	snap := b.Snapshot()
	if snap.State != "closed" {
		t.Errorf("expected closed, got %s", snap.State)
	}
	if snap.FailureThreshold != 5 {
		t.Errorf("expected failure threshold 5, got %d", snap.FailureThreshold)
	}
	if snap.MaxRequests != 1 {
		t.Errorf("expected max requests 1, got %d", snap.MaxRequests)
	}
}

func TestBreakerClosedToOpen(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout:          1 * time.Second,
	}, nil)

	// First 2 failures: still closed
	for i := 0; i < 2; i++ {
		done, err := b.Allow()
		if err != nil {
			t.Fatal("expected allowed in closed state")
		}
		done(fmt.Errorf("fail"))
	}

	snap := b.Snapshot()
	if snap.State != "closed" {
		t.Errorf("expected closed after 2 failures, got %s", snap.State)
	}

	// 3rd failure: transitions to open
	done, err := b.Allow()
	if err != nil {
		t.Fatal("expected allowed before recording 3rd failure")
	}
	done(fmt.Errorf("fail"))

	snap = b.Snapshot()
	if snap.State != "open" {
		t.Errorf("expected open after 3 failures, got %s", snap.State)
	}
}

func TestBreakerOpenRejectsRequests(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          10 * time.Second,
	}, nil)

	// Trip the breaker
	done, _ := b.Allow()
	done(fmt.Errorf("fail"))

	// Should be rejected
	_, err := b.Allow()
	if err == nil {
		t.Fatal("expected error when rejected")
	}
}

func TestBreakerOpenToHalfOpen(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          50 * time.Millisecond,
		MaxRequests:      1,
	}, nil)

	// Trip the breaker
	done, _ := b.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Should transition to half-open
	_, err := b.Allow()
	if err != nil {
		t.Fatal("expected allowed after timeout (half-open)")
	}

	snap := b.Snapshot()
	if snap.State != "half-open" {
		t.Errorf("expected half-open, got %s", snap.State)
	}
}

func TestBreakerHalfOpenLimitsRequests(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          50 * time.Millisecond,
		MaxRequests:      1,
	}, nil)

	// Trip the breaker
	done, _ := b.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// First request allowed (transitions to half-open)
	_, err := b.Allow()
	if err != nil {
		t.Fatal("expected first half-open request allowed")
	}

	// Second request should be rejected (max half-open reached)
	_, err = b.Allow()
	if err == nil {
		t.Fatal("expected second half-open request rejected")
	}
}

func TestBreakerHalfOpenToClosed(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 1,
		MaxRequests:      2,
		Timeout:          50 * time.Millisecond,
	}, nil)

	// Trip the breaker
	done, _ := b.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Allow transitions to half-open; need 2 consecutive successes (MaxRequests=2)
	done, _ = b.Allow()
	done(nil)
	done2, _ := b.Allow()
	done2(nil)

	snap := b.Snapshot()
	if snap.State != "closed" {
		t.Errorf("expected closed after 2 successes in half-open, got %s", snap.State)
	}
}

func TestBreakerHalfOpenToOpen(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          50 * time.Millisecond,
		MaxRequests:      2,
	}, nil)

	// Trip the breaker
	done, _ := b.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Allow transitions to half-open
	done, _ = b.Allow()
	done(fmt.Errorf("fail"))

	snap := b.Snapshot()
	if snap.State != "open" {
		t.Errorf("expected open after failure in half-open, got %s", snap.State)
	}
}

func TestBreakerSuccessResetsClosed(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout:          1 * time.Second,
	}, nil)

	// 2 failures
	done, _ := b.Allow()
	done(fmt.Errorf("fail"))
	done, _ = b.Allow()
	done(fmt.Errorf("fail"))

	// 1 success should reset consecutive failure count
	done, _ = b.Allow()
	done(nil)

	// 2 more failures should not open (reset happened)
	done, _ = b.Allow()
	done(fmt.Errorf("fail"))
	done, _ = b.Allow()
	done(fmt.Errorf("fail"))

	snap := b.Snapshot()
	if snap.State != "closed" {
		t.Errorf("expected closed (failures reset by success), got %s", snap.State)
	}
}

func TestBreakerMetrics(t *testing.T) {
	b := NewBreaker(config.CircuitBreakerConfig{
		FailureThreshold: 2,
		Timeout:          10 * time.Second,
	}, nil)

	done, _ := b.Allow()
	done(nil)
	done, _ = b.Allow()
	done(fmt.Errorf("fail"))
	done, _ = b.Allow()
	done(fmt.Errorf("fail"))

	// Now open, this should be rejected
	b.Allow()

	snap := b.Snapshot()
	if snap.TotalRequests != 4 {
		t.Errorf("expected 4 total requests, got %d", snap.TotalRequests)
	}
	if snap.TotalSuccesses != 1 {
		t.Errorf("expected 1 success, got %d", snap.TotalSuccesses)
	}
	if snap.TotalFailures != 2 {
		t.Errorf("expected 2 failures, got %d", snap.TotalFailures)
	}
	if snap.TotalRejected != 1 {
		t.Errorf("expected 1 rejected, got %d", snap.TotalRejected)
	}
}

func TestBreakerByRoute(t *testing.T) {
	br := NewBreakerByRoute()

	br.AddRoute("route1", config.CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout:          1 * time.Second,
	})
	br.AddRoute("route2", config.CircuitBreakerConfig{
		FailureThreshold: 5,
		Timeout:          2 * time.Second,
	})

	b1 := br.GetBreaker("route1")
	if b1 == nil {
		t.Fatal("expected breaker for route1")
	}

	b2 := br.GetBreaker("route2")
	if b2 == nil {
		t.Fatal("expected breaker for route2")
	}

	b3 := br.GetBreaker("route3")
	if b3 != nil {
		t.Fatal("expected nil for non-existent route3")
	}

	snapshots := br.Snapshots()
	if len(snapshots) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snapshots))
	}
}
