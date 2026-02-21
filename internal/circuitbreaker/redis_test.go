package circuitbreaker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wudi/gateway/internal/config"
)

func redisAvailable(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{
		Addr:        "localhost:6379",
		DialTimeout: 100 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	return client
}

func cleanupRedisKeys(t *testing.T, client *redis.Client, prefix string) {
	t.Helper()
	ctx := context.Background()
	var cursor uint64
	for {
		keys, next, err := client.Scan(ctx, cursor, prefix+"*", 100).Result()
		if err != nil {
			return
		}
		if len(keys) > 0 {
			client.Del(ctx, keys...)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func TestRedisBreaker_ClosedToOpen(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-c2o:"
	defer cleanupRedisKeys(t, client, prefix)

	rb := NewRedisBreaker("test-c2o", config.CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout:          1 * time.Second,
	}, client, nil)

	// 2 failures: still closed
	for i := 0; i < 2; i++ {
		done, err := rb.Allow()
		if err != nil {
			t.Fatalf("request %d: expected allowed, got %v", i, err)
		}
		done(fmt.Errorf("fail"))
	}

	snap := rb.Snapshot()
	if snap.State != "closed" {
		t.Errorf("expected closed after 2 failures, got %s", snap.State)
	}

	// 3rd failure: transitions to open
	done, err := rb.Allow()
	if err != nil {
		t.Fatal("expected allowed before recording 3rd failure")
	}
	done(fmt.Errorf("fail"))

	snap = rb.Snapshot()
	if snap.State != "open" {
		t.Errorf("expected open after 3 failures, got %s", snap.State)
	}
}

func TestRedisBreaker_OpenRejects(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-reject:"
	defer cleanupRedisKeys(t, client, prefix)

	rb := NewRedisBreaker("test-reject", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          10 * time.Second,
	}, client, nil)

	// Trip the breaker
	done, _ := rb.Allow()
	done(fmt.Errorf("fail"))

	// Should be rejected
	_, err := rb.Allow()
	if err == nil {
		t.Fatal("expected error when circuit breaker is open")
	}
}

func TestRedisBreaker_OpenToHalfOpen(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-ho:"
	defer cleanupRedisKeys(t, client, prefix)

	rb := NewRedisBreaker("test-ho", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          1 * time.Second,
		MaxRequests:      1,
	}, client, nil)

	// Trip the breaker
	done, _ := rb.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(1100 * time.Millisecond)

	// Should transition to half-open and allow
	_, err := rb.Allow()
	if err != nil {
		t.Fatalf("expected allowed after timeout (half-open), got %v", err)
	}

	snap := rb.Snapshot()
	if snap.State != "half-open" {
		t.Errorf("expected half-open, got %s", snap.State)
	}
}

func TestRedisBreaker_HalfOpenLimitsRequests(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-holimit:"
	defer cleanupRedisKeys(t, client, prefix)

	rb := NewRedisBreaker("test-holimit", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          1 * time.Second,
		MaxRequests:      1,
	}, client, nil)

	// Trip the breaker
	done, _ := rb.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(1100 * time.Millisecond)

	// First request: allowed (half-open)
	_, err := rb.Allow()
	if err != nil {
		t.Fatal("expected first half-open request allowed")
	}

	// Second request: rejected (max half-open reached)
	_, err = rb.Allow()
	if err == nil {
		t.Fatal("expected second half-open request rejected")
	}
}

func TestRedisBreaker_HalfOpenToClosed(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-ho2c:"
	defer cleanupRedisKeys(t, client, prefix)

	rb := NewRedisBreaker("test-ho2c", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          1 * time.Second,
		MaxRequests:      1,
	}, client, nil)

	// Trip the breaker
	done, _ := rb.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(1100 * time.Millisecond)

	// Half-open: success closes it
	done, err := rb.Allow()
	if err != nil {
		t.Fatal("expected allowed in half-open")
	}
	done(nil)

	snap := rb.Snapshot()
	if snap.State != "closed" {
		t.Errorf("expected closed after success in half-open, got %s", snap.State)
	}

	// Should be fully open now
	done2, err := rb.Allow()
	if err != nil {
		t.Fatal("expected allowed in closed state")
	}
	done2(nil)
}

func TestRedisBreaker_HalfOpenToOpen(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-ho2o:"
	defer cleanupRedisKeys(t, client, prefix)

	rb := NewRedisBreaker("test-ho2o", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          1 * time.Second,
		MaxRequests:      1,
	}, client, nil)

	// Trip the breaker
	done, _ := rb.Allow()
	done(fmt.Errorf("fail"))

	// Wait for timeout
	time.Sleep(1100 * time.Millisecond)

	// Half-open: failure reopens
	done, err := rb.Allow()
	if err != nil {
		t.Fatal("expected allowed in half-open")
	}
	done(fmt.Errorf("fail"))

	snap := rb.Snapshot()
	if snap.State != "open" {
		t.Errorf("expected open after failure in half-open, got %s", snap.State)
	}
}

func TestRedisBreaker_MultiInstance(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-multi:"
	defer cleanupRedisKeys(t, client, prefix)

	cfg := config.CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout:          10 * time.Second,
	}

	rb1 := NewRedisBreaker("test-multi", cfg, client, nil)
	rb2 := NewRedisBreaker("test-multi", cfg, client, nil)

	// Instance 1: 2 failures
	for i := 0; i < 2; i++ {
		done, err := rb1.Allow()
		if err != nil {
			t.Fatalf("rb1 request %d: expected allowed", i)
		}
		done(fmt.Errorf("fail"))
	}

	// Instance 2: 1 failure (should trip, threshold=3 total)
	done, err := rb2.Allow()
	if err != nil {
		t.Fatal("rb2: expected allowed before threshold")
	}
	done(fmt.Errorf("fail"))

	// Both instances should now see open state
	snap1 := rb1.Snapshot()
	snap2 := rb2.Snapshot()
	if snap1.State != "open" {
		t.Errorf("rb1: expected open, got %s", snap1.State)
	}
	if snap2.State != "open" {
		t.Errorf("rb2: expected open, got %s", snap2.State)
	}

	// Both instances should reject
	_, err = rb1.Allow()
	if err == nil {
		t.Fatal("rb1: expected rejection when open")
	}
	_, err = rb2.Allow()
	if err == nil {
		t.Fatal("rb2: expected rejection when open")
	}
}

func TestRedisBreaker_FailOpen(t *testing.T) {
	// Connect to a non-existent Redis
	client := redis.NewClient(&redis.Options{
		Addr:        "localhost:59999",
		DialTimeout: 50 * time.Millisecond,
	})

	rb := NewRedisBreaker("test-failopen", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          1 * time.Second,
	}, client, nil)

	// Should fail open: allow request even though Redis is unavailable
	done, err := rb.Allow()
	if err != nil {
		t.Fatalf("expected fail-open (allow), got error: %v", err)
	}
	// Done callback should be a no-op (not panic)
	done(nil)
	done(fmt.Errorf("fail"))
}

func TestRedisBreaker_StateChangeCallback(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-callback:"
	defer cleanupRedisKeys(t, client, prefix)

	var transitions []string
	rb := NewRedisBreaker("test-callback", config.CircuitBreakerConfig{
		FailureThreshold: 1,
		Timeout:          1 * time.Second,
	}, client, func(from, to string) {
		transitions = append(transitions, from+"->"+to)
	})

	// Trip: closed -> open
	done, _ := rb.Allow()
	done(fmt.Errorf("fail"))

	if len(transitions) != 1 || transitions[0] != "closed->open" {
		t.Errorf("expected [closed->open], got %v", transitions)
	}
}

func TestRedisBreaker_LifetimeCounters(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:cb:test-counters:"
	defer cleanupRedisKeys(t, client, prefix)

	rb := NewRedisBreaker("test-counters", config.CircuitBreakerConfig{
		FailureThreshold: 2,
		Timeout:          10 * time.Second,
	}, client, nil)

	// 1 success
	done, _ := rb.Allow()
	done(nil)
	// 2 failures (trips breaker)
	done, _ = rb.Allow()
	done(fmt.Errorf("fail"))
	done, _ = rb.Allow()
	done(fmt.Errorf("fail"))
	// 1 rejection
	rb.Allow()

	snap := rb.Snapshot()
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
	if snap.Mode != "distributed" {
		t.Errorf("expected mode=distributed, got %s", snap.Mode)
	}
}

func TestRedisBreaker_SnapshotMode(t *testing.T) {
	// Verify local breaker has mode=local
	lb := NewBreaker(config.CircuitBreakerConfig{}, nil)
	snap := lb.Snapshot()
	if snap.Mode != "local" {
		t.Errorf("local breaker: expected mode=local, got %s", snap.Mode)
	}
}
