package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestNewRedisLimiter(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	rl := NewRedisLimiter(RedisLimiterConfig{
		Client: client,
		Rate:   100,
		Period: time.Minute,
		Burst:  100,
	})

	if rl == nil {
		t.Fatal("NewRedisLimiter() returned nil")
	}
	if rl.rate != 100 {
		t.Errorf("expected rate 100, got %d", rl.rate)
	}
	if rl.window != time.Minute {
		t.Errorf("expected window 1m, got %v", rl.window)
	}
	if rl.prefix != "gw:rl:" {
		t.Errorf("expected default prefix 'gw:rl:', got %q", rl.prefix)
	}
}

func TestNewRedisLimiter_Defaults(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	rl := NewRedisLimiter(RedisLimiterConfig{
		Client: client,
		Rate:   50,
	})

	if rl.window != time.Minute {
		t.Errorf("expected default period 1m, got %v", rl.window)
	}
	if rl.burst != 50 {
		t.Errorf("expected burst to default to rate (50), got %d", rl.burst)
	}
	if rl.prefix != "gw:rl:" {
		t.Errorf("expected default prefix 'gw:rl:', got %q", rl.prefix)
	}
}

func TestNewRedisLimiter_CustomPrefix(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	rl := NewRedisLimiter(RedisLimiterConfig{
		Client: client,
		Prefix: "custom:",
		Rate:   10,
	})

	if rl.prefix != "custom:" {
		t.Errorf("expected prefix 'custom:', got %q", rl.prefix)
	}
}

func TestNewRedisLimiter_PerIP(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	rl := NewRedisLimiter(RedisLimiterConfig{
		Client: client,
		Rate:   10,
		PerIP:  true,
	})

	if !rl.perIP {
		t.Error("expected perIP to be true")
	}
}

func TestRedisLimiter_FailOpen(t *testing.T) {
	// Client pointing to a non-existent Redis — middleware should fail open
	client := redis.NewClient(&redis.Options{
		Addr:        "localhost:1", // unlikely to have Redis here
		DialTimeout: 10 * time.Millisecond,
	})

	rl := NewRedisLimiter(RedisLimiterConfig{
		Client: client,
		Rate:   1,
		Period: time.Second,
	})

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := rl.Middleware()(inner)

	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if !called {
		t.Error("expected next handler to be called (fail-open)")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 on fail-open, got %d", w.Code)
	}
}

func TestRedisLimiter_MiddlewareCreation(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

	rl := NewRedisLimiter(RedisLimiterConfig{
		Client: client,
		Rate:   10,
	})

	mw := rl.Middleware()
	if mw == nil {
		t.Fatal("Middleware() returned nil")
	}
}

func TestSlidingWindowScript(t *testing.T) {
	// Verify the Lua script object was created
	if slidingWindowScript == nil {
		t.Fatal("slidingWindowScript is nil")
	}
}
