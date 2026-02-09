package cache

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
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

func TestRedisStore_GetSet(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:test:getset:"
	defer cleanupRedisKeys(t, client, prefix)

	store := NewRedisStore(client, prefix, 30*time.Second)

	entry := &Entry{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"ok":true}`),
	}

	store.Set("key1", entry)

	got, ok := store.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", got.StatusCode)
	}
	if string(got.Body) != `{"ok":true}` {
		t.Errorf("unexpected body: %s", got.Body)
	}
	if got.Headers.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type header, got %v", got.Headers)
	}
}

func TestRedisStore_Miss(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:test:miss:"
	defer cleanupRedisKeys(t, client, prefix)

	store := NewRedisStore(client, prefix, 30*time.Second)

	_, ok := store.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestRedisStore_TTLExpiry(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:test:ttl:"
	defer cleanupRedisKeys(t, client, prefix)

	store := NewRedisStore(client, prefix, 1*time.Second)

	store.Set("expiring", &Entry{StatusCode: 200, Body: []byte("data")})

	// Verify it's there
	_, ok := store.Get("expiring")
	if !ok {
		t.Fatal("expected cache hit before expiry")
	}

	time.Sleep(1500 * time.Millisecond)

	_, ok = store.Get("expiring")
	if ok {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestRedisStore_Delete(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:test:delete:"
	defer cleanupRedisKeys(t, client, prefix)

	store := NewRedisStore(client, prefix, 30*time.Second)

	store.Set("key1", &Entry{StatusCode: 200, Body: []byte("data")})
	store.Delete("key1")

	_, ok := store.Get("key1")
	if ok {
		t.Fatal("expected cache miss after delete")
	}
}

func TestRedisStore_Purge(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:test:purge:"
	defer cleanupRedisKeys(t, client, prefix)

	store := NewRedisStore(client, prefix, 30*time.Second)

	for i := 0; i < 5; i++ {
		store.Set(string(rune('a'+i)), &Entry{StatusCode: 200, Body: []byte("data")})
	}

	store.Purge()

	stats := store.Stats()
	if stats.Size != 0 {
		t.Errorf("expected size 0 after purge, got %d", stats.Size)
	}
}

func TestRedisStore_Stats(t *testing.T) {
	client := redisAvailable(t)
	prefix := "gw:test:stats:"
	defer cleanupRedisKeys(t, client, prefix)

	store := NewRedisStore(client, prefix, 30*time.Second)

	store.Set("key1", &Entry{StatusCode: 200, Body: []byte("data")})
	store.Set("key2", &Entry{StatusCode: 200, Body: []byte("data")})

	stats := store.Stats()
	if stats.Size != 2 {
		t.Errorf("expected size 2, got %d", stats.Size)
	}
	if stats.MaxSize != 0 {
		t.Errorf("expected max_size 0 for Redis, got %d", stats.MaxSize)
	}
}

func TestRedisStore_FailOpen(t *testing.T) {
	// Client pointing to a non-existent Redis — operations should fail silently
	client := redis.NewClient(&redis.Options{
		Addr:        "localhost:1", // unlikely to have Redis here
		DialTimeout: 10 * time.Millisecond,
	})
	prefix := "gw:test:failopen:"

	store := NewRedisStore(client, prefix, 30*time.Second)

	// Set should not panic
	store.Set("key1", &Entry{StatusCode: 200, Body: []byte("data")})

	// Get should return miss, not error
	_, ok := store.Get("key1")
	if ok {
		t.Fatal("expected miss on unreachable Redis")
	}

	// Delete should not panic
	store.Delete("key1")

	// Purge should not panic
	store.Purge()

	// Stats should return empty
	stats := store.Stats()
	if stats.Size != 0 {
		t.Errorf("expected size 0 on unreachable Redis, got %d", stats.Size)
	}
}
