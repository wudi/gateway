package retry

import (
	"testing"
	"time"
)

func TestBudget_AllowsWithinRatio(t *testing.T) {
	b := NewBudget(0.5, 0, 10*time.Second)

	// Record 10 requests
	for i := 0; i < 10; i++ {
		b.RecordRequest()
	}

	// Should allow up to ~50% retries (5 out of 10)
	for i := 0; i < 4; i++ {
		if !b.AllowRetry() {
			t.Fatalf("retry %d should be allowed", i)
		}
		b.RecordRetry()
	}
}

func TestBudget_DeniesOverRatio(t *testing.T) {
	b := NewBudget(0.2, 0, 10*time.Second)

	// Record 10 requests
	for i := 0; i < 10; i++ {
		b.RecordRequest()
	}

	// Record 2 retries (= 20% of 10)
	b.RecordRetry()
	b.RecordRetry()

	// Next retry should be denied (would be 3/10 = 30% > 20%)
	if b.AllowRetry() {
		t.Error("retry should be denied, budget exhausted")
	}
}

func TestBudget_MinRetriesBypass(t *testing.T) {
	b := NewBudget(0.01, 10, 10*time.Second) // very low ratio but 10 min retries/sec

	// Record 100 requests
	for i := 0; i < 100; i++ {
		b.RecordRequest()
	}

	// Record some retries — should still be allowed because of min_retries
	// 10 retries/sec * 10s window = 100 retries allowed as minimum
	for i := 0; i < 50; i++ {
		if !b.AllowRetry() {
			t.Fatalf("retry %d should be allowed by min_retries bypass", i)
		}
		b.RecordRetry()
	}
}

func TestBudget_EmptyBudgetAllows(t *testing.T) {
	b := NewBudget(0.2, 0, 10*time.Second)

	// No requests recorded yet — should allow
	if !b.AllowRetry() {
		t.Error("empty budget should allow retries")
	}
}

func TestBudget_WindowAdvancement(t *testing.T) {
	// Very short window for testing
	b := NewBudget(0.2, 0, 100*time.Millisecond)

	// Record requests and max out the budget
	for i := 0; i < 10; i++ {
		b.RecordRequest()
	}
	b.RecordRetry()
	b.RecordRetry()

	// Should be at budget limit
	if b.AllowRetry() {
		t.Error("should be at budget limit")
	}

	// Wait for window to advance
	time.Sleep(120 * time.Millisecond)

	// After window advancement, old buckets should be cleared
	// Record fresh requests
	for i := 0; i < 10; i++ {
		b.RecordRequest()
	}

	// Should allow retries again
	if !b.AllowRetry() {
		t.Error("should allow retries after window advancement")
	}
}

func TestBudget_DefaultWindow(t *testing.T) {
	b := NewBudget(0.5, 3, 0) // 0 window → default 10s
	if b.window != 10*time.Second {
		t.Errorf("expected default window of 10s, got %v", b.window)
	}
}
