package cache

import (
	"net/http"
	"testing"
	"time"
)

func TestMemoryStore_SetWithTags(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	entry := &Entry{StatusCode: 200, Headers: http.Header{}, Body: []byte("data")}
	s.SetWithTags("key1", entry, []string{"tag-a", "tag-b"})

	// Verify entry is stored
	got, ok := s.Get("key1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.StatusCode != 200 {
		t.Errorf("expected 200, got %d", got.StatusCode)
	}
}

func TestMemoryStore_DeleteByTags_SingleTag(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"product"})
	s.SetWithTags("key2", &Entry{StatusCode: 200, Body: []byte("2")}, []string{"product"})
	s.SetWithTags("key3", &Entry{StatusCode: 200, Body: []byte("3")}, []string{"user"})

	count := s.DeleteByTags([]string{"product"})
	if count != 2 {
		t.Errorf("expected 2 deleted, got %d", count)
	}

	// key1, key2 should be gone
	if _, ok := s.Get("key1"); ok {
		t.Error("expected key1 to be deleted")
	}
	if _, ok := s.Get("key2"); ok {
		t.Error("expected key2 to be deleted")
	}

	// key3 should remain
	if _, ok := s.Get("key3"); !ok {
		t.Error("expected key3 to still exist")
	}
}

func TestMemoryStore_DeleteByTags_MultipleTags(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"tag-a"})
	s.SetWithTags("key2", &Entry{StatusCode: 200, Body: []byte("2")}, []string{"tag-b"})
	s.SetWithTags("key3", &Entry{StatusCode: 200, Body: []byte("3")}, []string{"tag-c"})

	count := s.DeleteByTags([]string{"tag-a", "tag-b"})
	if count != 2 {
		t.Errorf("expected 2 deleted, got %d", count)
	}

	if _, ok := s.Get("key3"); !ok {
		t.Error("expected key3 to still exist")
	}
}

func TestMemoryStore_DeleteByTags_OverlappingTags(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	// key1 has both tags — should only be counted once
	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"tag-a", "tag-b"})
	s.SetWithTags("key2", &Entry{StatusCode: 200, Body: []byte("2")}, []string{"tag-a"})

	count := s.DeleteByTags([]string{"tag-a", "tag-b"})
	if count != 2 {
		t.Errorf("expected 2 deleted (unique keys), got %d", count)
	}
}

func TestMemoryStore_DeleteByTags_NonexistentTag(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"tag-a"})

	count := s.DeleteByTags([]string{"nonexistent"})
	if count != 0 {
		t.Errorf("expected 0 deleted, got %d", count)
	}

	if _, ok := s.Get("key1"); !ok {
		t.Error("expected key1 to still exist")
	}
}

func TestMemoryStore_Delete_CleansTagIndex(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"tag-a"})

	// Delete the key
	s.Delete("key1")

	// The tag should no longer reference the key
	count := s.DeleteByTags([]string{"tag-a"})
	if count != 0 {
		t.Errorf("expected 0 after delete cleaned index, got %d", count)
	}
}

func TestMemoryStore_Eviction_CleansTagIndex(t *testing.T) {
	s := NewMemoryStore(2, time.Minute) // LRU with size 2

	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"evict-tag"})
	s.SetWithTags("key2", &Entry{StatusCode: 200, Body: []byte("2")}, []string{"keep-tag"})

	// Adding key3 should evict key1
	s.SetWithTags("key3", &Entry{StatusCode: 200, Body: []byte("3")}, []string{"keep-tag"})

	// Purging "evict-tag" should find no keys
	count := s.DeleteByTags([]string{"evict-tag"})
	if count != 0 {
		t.Errorf("expected 0 (evicted key cleaned from tag index), got %d", count)
	}
}

func TestMemoryStore_SetWithTags_OverwriteUpdatesIndex(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"old-tag"})
	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("2")}, []string{"new-tag"})

	// old-tag should no longer reference key1
	count := s.DeleteByTags([]string{"old-tag"})
	if count != 0 {
		t.Errorf("expected 0 for old-tag after overwrite, got %d", count)
	}

	// new-tag should reference key1
	count = s.DeleteByTags([]string{"new-tag"})
	if count != 1 {
		t.Errorf("expected 1 for new-tag, got %d", count)
	}
}

func TestMemoryStore_SetWithTags_NoTags(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)

	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, nil)

	// Entry should still be stored
	if _, ok := s.Get("key1"); !ok {
		t.Error("expected cache hit")
	}
}

func TestMemoryStore_DeleteByTags_EmptyTags(t *testing.T) {
	s := NewMemoryStore(100, time.Minute)
	s.SetWithTags("key1", &Entry{StatusCode: 200, Body: []byte("1")}, []string{"tag-a"})

	count := s.DeleteByTags(nil)
	if count != 0 {
		t.Errorf("expected 0 for nil tags, got %d", count)
	}
}
