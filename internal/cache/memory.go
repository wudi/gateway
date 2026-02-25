package cache

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	expirable "github.com/hashicorp/golang-lru/v2/expirable"
)

// MemoryStore is an in-memory LRU cache implementing Store.
type MemoryStore struct {
	lru       *expirable.LRU[string, *Entry]
	mu        sync.Mutex // protects DeleteByPrefix atomicity and tag indexes
	evictions atomic.Int64
	maxSize   int
	tagIndex  map[string]map[string]struct{} // tag → set of cache keys
	keyTags   map[string][]string            // key → tags
}

// NewMemoryStore creates a new in-memory LRU store with the given max size and TTL.
func NewMemoryStore(maxSize int, ttl time.Duration) *MemoryStore {
	if maxSize <= 0 {
		maxSize = 1000
	}
	s := &MemoryStore{
		maxSize:  maxSize,
		tagIndex: make(map[string]map[string]struct{}),
		keyTags:  make(map[string][]string),
	}
	s.lru = expirable.NewLRU[string, *Entry](maxSize, func(key string, value *Entry) {
		s.evictions.Add(1)
		s.cleanTagIndexForKey(key)
	}, ttl)
	return s
}

func (s *MemoryStore) Get(key string) (*Entry, bool) {
	return s.lru.Get(key)
}

func (s *MemoryStore) Set(key string, entry *Entry) {
	s.lru.Add(key, entry)
}

func (s *MemoryStore) Delete(key string) {
	s.mu.Lock()
	s.cleanTagIndexForKey(key)
	s.mu.Unlock()
	s.lru.Remove(key)
}

// SetWithTags stores an entry and updates tag indexes.
func (s *MemoryStore) SetWithTags(key string, entry *Entry, tags []string) {
	s.mu.Lock()
	// Clean old tags for this key
	s.cleanTagIndexForKey(key)
	// Add new tags
	if len(tags) > 0 {
		s.keyTags[key] = tags
		for _, tag := range tags {
			if s.tagIndex[tag] == nil {
				s.tagIndex[tag] = make(map[string]struct{})
			}
			s.tagIndex[tag][key] = struct{}{}
		}
	}
	s.mu.Unlock()
	s.lru.Add(key, entry)
}

// DeleteByTags removes all entries matching any of the given tags. Returns count of deleted keys.
func (s *MemoryStore) DeleteByTags(tags []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Collect all unique keys for the given tags
	toDelete := make(map[string]struct{})
	for _, tag := range tags {
		if keys, ok := s.tagIndex[tag]; ok {
			for k := range keys {
				toDelete[k] = struct{}{}
			}
		}
	}

	// Delete each key from LRU and clean its tag indexes
	for key := range toDelete {
		s.cleanTagIndexForKey(key)
		s.lru.Remove(key)
	}

	return len(toDelete)
}

// cleanTagIndexForKey removes all tag index entries for a key. Must be called with mu held.
func (s *MemoryStore) cleanTagIndexForKey(key string) {
	if tags, ok := s.keyTags[key]; ok {
		for _, tag := range tags {
			if keys, ok := s.tagIndex[tag]; ok {
				delete(keys, key)
				if len(keys) == 0 {
					delete(s.tagIndex, tag)
				}
			}
		}
		delete(s.keyTags, key)
	}
}

func (s *MemoryStore) DeleteByPrefix(prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, key := range s.lru.Keys() {
		if strings.HasPrefix(key, prefix) {
			s.lru.Remove(key)
		}
	}
}

func (s *MemoryStore) Purge() {
	s.lru.Purge()
}

func (s *MemoryStore) Stats() StoreStats {
	return StoreStats{
		Size:      s.lru.Len(),
		MaxSize:   s.maxSize,
		Evictions: s.evictions.Load(),
	}
}
