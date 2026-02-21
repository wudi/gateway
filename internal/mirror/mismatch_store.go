package mirror

import (
	"sync"
	"sync/atomic"
	"time"
)

const defaultMaxMismatches = 100

// MismatchEntry represents a single captured mismatch between primary and mirror responses.
type MismatchEntry struct {
	Timestamp time.Time  `json:"timestamp"`
	Method    string     `json:"method"`
	Path      string     `json:"path"`
	Backend   string     `json:"backend"`
	Detail    DiffDetail `json:"detail"`
	DiffTypes []string   `json:"diff_types"`
}

// MismatchStore is a thread-safe ring buffer that stores recent mismatch entries.
type MismatchStore struct {
	mu       sync.Mutex
	entries  []MismatchEntry
	capacity int
	idx      int
	full     bool
	total    atomic.Int64
}

// NewMismatchStore creates a MismatchStore with the given capacity.
// If capacity <= 0, defaultMaxMismatches is used.
func NewMismatchStore(capacity int) *MismatchStore {
	if capacity <= 0 {
		capacity = defaultMaxMismatches
	}
	return &MismatchStore{
		entries:  make([]MismatchEntry, 0, capacity),
		capacity: capacity,
	}
}

// Add adds an entry to the ring buffer.
func (s *MismatchStore) Add(entry MismatchEntry) {
	s.total.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.entries) < s.capacity {
		s.entries = append(s.entries, entry)
	} else {
		s.entries[s.idx] = entry
		s.full = true
	}
	s.idx = (s.idx + 1) % s.capacity
}

// Entries returns all stored entries, newest first.
func (s *MismatchStore) Entries() []MismatchEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	n := len(s.entries)
	if n == 0 {
		return nil
	}

	result := make([]MismatchEntry, n)
	// Newest entry is at idx-1 (wrapping)
	for i := 0; i < n; i++ {
		srcIdx := (s.idx - 1 - i + n) % n
		result[i] = s.entries[srcIdx]
	}
	return result
}

// Clear removes all stored entries (total count is preserved).
func (s *MismatchStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make([]MismatchEntry, 0, s.capacity)
	s.idx = 0
	s.full = false
}

// Total returns the total number of mismatches ever recorded.
func (s *MismatchStore) Total() int64 {
	return s.total.Load()
}

// Size returns the current number of stored entries.
func (s *MismatchStore) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// MismatchSnapshot is a point-in-time summary of the mismatch store.
type MismatchSnapshot struct {
	TotalMismatches int64           `json:"total_mismatches"`
	StoredCount     int             `json:"stored_count"`
	Capacity        int             `json:"capacity"`
	Entries         []MismatchEntry `json:"entries"`
}

// Snapshot returns a point-in-time summary of the mismatch store.
func (s *MismatchStore) Snapshot() MismatchSnapshot {
	entries := s.Entries()
	if entries == nil {
		entries = []MismatchEntry{}
	}
	return MismatchSnapshot{
		TotalMismatches: s.Total(),
		StoredCount:     len(entries),
		Capacity:        s.capacity,
		Entries:         entries,
	}
}
