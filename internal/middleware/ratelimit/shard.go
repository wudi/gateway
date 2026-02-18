package ratelimit

import (
	"hash/fnv"
	"sync"
)

const numShards = 64

// shard is a single partition of the sharded map.
type shard[V any] struct {
	mu    sync.Mutex
	items map[string]V
}

// shardedMap is a concurrent map split into fixed shards to reduce lock contention.
type shardedMap[V any] struct {
	shards [numShards]shard[V]
}

func newShardedMap[V any]() *shardedMap[V] {
	var m shardedMap[V]
	for i := range m.shards {
		m.shards[i].items = make(map[string]V)
	}
	return &m
}

func (m *shardedMap[V]) getShard(key string) *shard[V] {
	h := fnv.New32a()
	h.Write([]byte(key))
	return &m.shards[h.Sum32()%numShards]
}

// getOrCreate returns the value for key, creating it with init if absent.
// The shard lock is held during init; keep init cheap.
func (m *shardedMap[V]) getOrCreate(key string, init func() V) V {
	s := m.getShard(key)
	s.mu.Lock()
	v, ok := s.items[key]
	if !ok {
		v = init()
		s.items[key] = v
	}
	s.mu.Unlock()
	return v
}

// get returns the value for key and whether it existed.
func (m *shardedMap[V]) get(key string) (V, bool) {
	s := m.getShard(key)
	s.mu.Lock()
	v, ok := s.items[key]
	s.mu.Unlock()
	return v, ok
}

// set stores a value for key.
func (m *shardedMap[V]) set(key string, v V) {
	s := m.getShard(key)
	s.mu.Lock()
	s.items[key] = v
	s.mu.Unlock()
}

// deleteFunc iterates all shards and deletes entries for which fn returns true.
func (m *shardedMap[V]) deleteFunc(fn func(key string, v V) bool) {
	for i := range m.shards {
		s := &m.shards[i]
		s.mu.Lock()
		for k, v := range s.items {
			if fn(k, v) {
				delete(s.items, k)
			}
		}
		s.mu.Unlock()
	}
}
