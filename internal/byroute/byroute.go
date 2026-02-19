package byroute

import "sync"

// Manager is a generic thread-safe per-route object store.
// It replaces the hand-written XxxByRoute structs that all follow
// the same map[string]*T + sync.RWMutex pattern.
type Manager[T any] struct {
	items map[string]T
	mu    sync.RWMutex
}

// New creates a new Manager.
func New[T any]() *Manager[T] {
	return &Manager[T]{}
}

// Add stores an item for the given route ID.
func (m *Manager[T]) Add(routeID string, item T) {
	m.mu.Lock()
	if m.items == nil {
		m.items = make(map[string]T)
	}
	m.items[routeID] = item
	m.mu.Unlock()
}

// Get retrieves the item for the given route ID.
func (m *Manager[T]) Get(routeID string) (_ T, ok bool) {
	m.mu.RLock()
	v, ok := m.items[routeID]
	m.mu.RUnlock()
	return v, ok
}

// RouteIDs returns all route IDs that have items stored.
func (m *Manager[T]) RouteIDs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.items))
	for id := range m.items {
		ids = append(ids, id)
	}
	return ids
}

// Range iterates over all items. Return false from fn to stop early.
func (m *Manager[T]) Range(fn func(id string, item T) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, item := range m.items {
		if !fn(id, item) {
			break
		}
	}
}

// Len returns the number of stored items.
func (m *Manager[T]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

// Clear removes all stored items.
func (m *Manager[T]) Clear() {
	m.mu.Lock()
	m.items = nil
	m.mu.Unlock()
}
