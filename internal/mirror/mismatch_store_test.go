package mirror

import (
	"fmt"
	"testing"
	"time"
)

func TestMismatchStore_AddAndRetrieve(t *testing.T) {
	store := NewMismatchStore(5)

	store.Add(MismatchEntry{
		Timestamp: time.Now(),
		Method:    "GET",
		Path:      "/test",
		Backend:   "http://shadow:8080",
	})

	entries := store.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Path != "/test" {
		t.Errorf("expected path /test, got %s", entries[0].Path)
	}
	if store.Total() != 1 {
		t.Errorf("expected total 1, got %d", store.Total())
	}
	if store.Size() != 1 {
		t.Errorf("expected size 1, got %d", store.Size())
	}
}

func TestMismatchStore_RingBufferOverflow(t *testing.T) {
	store := NewMismatchStore(3)

	for i := 0; i < 5; i++ {
		store.Add(MismatchEntry{
			Timestamp: time.Now(),
			Path:      fmt.Sprintf("/path/%d", i),
		})
	}

	if store.Total() != 5 {
		t.Errorf("expected total 5, got %d", store.Total())
	}
	if store.Size() != 3 {
		t.Errorf("expected size 3, got %d", store.Size())
	}

	entries := store.Entries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// Newest first: /path/4, /path/3, /path/2
	if entries[0].Path != "/path/4" {
		t.Errorf("newest entry should be /path/4, got %s", entries[0].Path)
	}
	if entries[1].Path != "/path/3" {
		t.Errorf("second entry should be /path/3, got %s", entries[1].Path)
	}
	if entries[2].Path != "/path/2" {
		t.Errorf("oldest entry should be /path/2, got %s", entries[2].Path)
	}
}

func TestMismatchStore_Clear(t *testing.T) {
	store := NewMismatchStore(10)

	for i := 0; i < 5; i++ {
		store.Add(MismatchEntry{Path: fmt.Sprintf("/path/%d", i)})
	}

	store.Clear()

	if store.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", store.Size())
	}
	entries := store.Entries()
	if entries != nil {
		t.Errorf("expected nil entries after clear, got %d", len(entries))
	}

	// Total is preserved
	if store.Total() != 5 {
		t.Errorf("total should be preserved after clear, got %d", store.Total())
	}
}

func TestMismatchStore_Empty(t *testing.T) {
	store := NewMismatchStore(10)

	entries := store.Entries()
	if entries != nil {
		t.Error("expected nil entries for empty store")
	}
	if store.Total() != 0 {
		t.Errorf("expected total 0, got %d", store.Total())
	}
	if store.Size() != 0 {
		t.Errorf("expected size 0, got %d", store.Size())
	}
}

func TestMismatchStore_DefaultCapacity(t *testing.T) {
	store := NewMismatchStore(0)

	// Should use defaultMaxMismatches (100)
	for i := 0; i < 150; i++ {
		store.Add(MismatchEntry{Path: fmt.Sprintf("/path/%d", i)})
	}

	if store.Size() != 100 {
		t.Errorf("expected size 100 (default), got %d", store.Size())
	}
	if store.Total() != 150 {
		t.Errorf("expected total 150, got %d", store.Total())
	}
}

func TestMismatchStore_Snapshot(t *testing.T) {
	store := NewMismatchStore(5)

	for i := 0; i < 3; i++ {
		store.Add(MismatchEntry{Path: fmt.Sprintf("/path/%d", i)})
	}

	snap := store.Snapshot()
	if snap.TotalMismatches != 3 {
		t.Errorf("expected total 3, got %d", snap.TotalMismatches)
	}
	if snap.StoredCount != 3 {
		t.Errorf("expected stored 3, got %d", snap.StoredCount)
	}
	if snap.Capacity != 5 {
		t.Errorf("expected capacity 5, got %d", snap.Capacity)
	}
	if len(snap.Entries) != 3 {
		t.Errorf("expected 3 entries, got %d", len(snap.Entries))
	}
}

func TestMismatchStore_SnapshotEmpty(t *testing.T) {
	store := NewMismatchStore(5)
	snap := store.Snapshot()

	if snap.Entries == nil {
		t.Error("snapshot entries should be non-nil empty slice")
	}
	if len(snap.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(snap.Entries))
	}
}

func TestMismatchStore_NewestFirst(t *testing.T) {
	store := NewMismatchStore(10)

	now := time.Now()
	for i := 0; i < 5; i++ {
		store.Add(MismatchEntry{
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Path:      fmt.Sprintf("/path/%d", i),
		})
	}

	entries := store.Entries()
	// Verify newest first ordering
	for i := 0; i < len(entries)-1; i++ {
		if entries[i].Timestamp.Before(entries[i+1].Timestamp) {
			t.Errorf("entry %d (t=%v) should be newer than entry %d (t=%v)",
				i, entries[i].Timestamp, i+1, entries[i+1].Timestamp)
		}
	}
}
