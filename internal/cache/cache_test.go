package cache

import (
	"net/http"
	"testing"
	"time"
)

func TestCacheGetSet(t *testing.T) {
	c := New(NewMemoryStore(10, 1*time.Minute))

	entry := &Entry{
		StatusCode: 200,
		Headers:    http.Header{"Content-Type": {"application/json"}},
		Body:       []byte(`{"ok":true}`),
	}

	c.Set("key1", entry)

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", got.StatusCode)
	}
	if string(got.Body) != `{"ok":true}` {
		t.Errorf("unexpected body: %s", got.Body)
	}
}

func TestCacheMiss(t *testing.T) {
	c := New(NewMemoryStore(10, 1*time.Minute))

	_, ok := c.Get("nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestCacheExpiry(t *testing.T) {
	c := New(NewMemoryStore(10, 10*time.Millisecond))

	entry := &Entry{
		StatusCode: 200,
		Body:       []byte("data"),
	}

	c.Set("expired", entry)

	time.Sleep(50 * time.Millisecond)

	_, ok := c.Get("expired")
	if ok {
		t.Fatal("expected cache miss for expired entry")
	}
}

func TestCacheLRUEviction(t *testing.T) {
	c := New(NewMemoryStore(3, 1*time.Minute))

	for i := 0; i < 3; i++ {
		c.Set(string(rune('a'+i)), &Entry{
			StatusCode: 200,
			Body:       []byte("data"),
		})
	}

	// Add a 4th element, should evict 'a' (least recently used)
	c.Set("d", &Entry{
		StatusCode: 200,
		Body:       []byte("data"),
	})

	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected 'a' to be evicted")
	}

	_, ok = c.Get("b")
	if !ok {
		t.Fatal("expected 'b' to still exist")
	}
}

func TestCacheLRUAccessOrder(t *testing.T) {
	c := New(NewMemoryStore(3, 1*time.Minute))

	for i := 0; i < 3; i++ {
		c.Set(string(rune('a'+i)), &Entry{
			StatusCode: 200,
			Body:       []byte("data"),
		})
	}

	// Access 'a' to make it recently used
	c.Get("a")

	// Add a 4th element, should evict 'b' (now least recently used)
	c.Set("d", &Entry{
		StatusCode: 200,
		Body:       []byte("data"),
	})

	_, ok := c.Get("a")
	if !ok {
		t.Fatal("expected 'a' to still exist (was accessed)")
	}

	_, ok = c.Get("b")
	if ok {
		t.Fatal("expected 'b' to be evicted")
	}
}

func TestCacheDelete(t *testing.T) {
	c := New(NewMemoryStore(10, 1*time.Minute))

	c.Set("key1", &Entry{
		StatusCode: 200,
		Body:       []byte("data"),
	})

	c.Delete("key1")

	_, ok := c.Get("key1")
	if ok {
		t.Fatal("expected cache miss after delete")
	}
}

func TestCachePurge(t *testing.T) {
	c := New(NewMemoryStore(10, 1*time.Minute))

	for i := 0; i < 5; i++ {
		c.Set(string(rune('a'+i)), &Entry{
			StatusCode: 200,
			Body:       []byte("data"),
		})
	}

	c.Purge()

	stats := c.Stats()
	if stats.Size != 0 {
		t.Errorf("expected size 0 after purge, got %d", stats.Size)
	}
}

func TestCacheStats(t *testing.T) {
	c := New(NewMemoryStore(10, 1*time.Minute))

	c.Set("key1", &Entry{
		StatusCode: 200,
		Body:       []byte("data"),
	})

	c.Get("key1") // hit
	c.Get("key2") // miss

	stats := c.Stats()
	if stats.Hits != 1 {
		t.Errorf("expected 1 hit, got %d", stats.Hits)
	}
	if stats.Misses != 1 {
		t.Errorf("expected 1 miss, got %d", stats.Misses)
	}
	if stats.Size != 1 {
		t.Errorf("expected size 1, got %d", stats.Size)
	}
}

func TestCacheUpdateExisting(t *testing.T) {
	c := New(NewMemoryStore(10, 1*time.Minute))

	c.Set("key1", &Entry{
		StatusCode: 200,
		Body:       []byte("v1"),
	})

	c.Set("key1", &Entry{
		StatusCode: 201,
		Body:       []byte("v2"),
	})

	got, ok := c.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.StatusCode != 201 {
		t.Errorf("expected status 201, got %d", got.StatusCode)
	}

	stats := c.Stats()
	if stats.Size != 1 {
		t.Errorf("expected size 1 after update, got %d", stats.Size)
	}
}
