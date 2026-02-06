package trafficshape

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestThrottler_BasicAllow(t *testing.T) {
	throttler := NewThrottler(100, 10, 5*time.Second, false)
	r := httptest.NewRequest("GET", "/", nil)

	// Should allow a burst of requests instantly
	for i := 0; i < 10; i++ {
		if err := throttler.Throttle(context.Background(), r); err != nil {
			t.Fatalf("request %d: unexpected error: %v", i, err)
		}
	}

	snap := throttler.Snapshot()
	if snap.TotalRequests != 10 {
		t.Errorf("expected 10 total requests, got %d", snap.TotalRequests)
	}
}

func TestThrottler_TimedOut(t *testing.T) {
	// Very low rate, small burst, very short maxWait
	throttler := NewThrottler(1, 1, 10*time.Millisecond, false)
	r := httptest.NewRequest("GET", "/", nil)

	// First request uses the burst token
	if err := throttler.Throttle(context.Background(), r); err != nil {
		t.Fatalf("first request: unexpected error: %v", err)
	}

	// Second request should time out (1 req/s rate, 10ms max wait)
	err := throttler.Throttle(context.Background(), r)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	snap := throttler.Snapshot()
	if snap.TotalTimedOut == 0 {
		t.Error("expected at least 1 timed out request")
	}
}

func TestThrottler_PerIP(t *testing.T) {
	throttler := NewThrottler(1, 1, 10*time.Millisecond, true)

	r1 := httptest.NewRequest("GET", "/", nil)
	r1.RemoteAddr = "10.0.0.1:1234"

	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "10.0.0.2:1234"

	// Both IPs should get their burst tokens
	if err := throttler.Throttle(context.Background(), r1); err != nil {
		t.Fatalf("ip1: unexpected error: %v", err)
	}
	if err := throttler.Throttle(context.Background(), r2); err != nil {
		t.Fatalf("ip2: unexpected error: %v", err)
	}

	// Second request from ip1 should time out
	err := throttler.Throttle(context.Background(), r1)
	if err == nil {
		t.Fatal("expected timeout for ip1 second request")
	}
}

func TestThrottler_Concurrent(t *testing.T) {
	throttler := NewThrottler(1000, 100, 5*time.Second, false)
	r := httptest.NewRequest("GET", "/", nil)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			throttler.Throttle(context.Background(), r)
		}()
	}
	wg.Wait()

	snap := throttler.Snapshot()
	if snap.TotalRequests != 50 {
		t.Errorf("expected 50 total requests, got %d", snap.TotalRequests)
	}
}
