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

// Lookup returns the item for the given route ID, or the zero value if not found.
func (m *Manager[T]) Lookup(routeID string) T {
	v, _ := m.Get(routeID)
	return v
}

// Clear removes all stored items.
func (m *Manager[T]) Clear() {
	m.mu.Lock()
	m.items = nil
	m.mu.Unlock()
}

// ForEach calls fn on every stored item. This replaces the hand-written
// CloseAll/StopAll boilerplate that iterates via Range just to call a method.
func ForEach[T any](m *Manager[T], fn func(T)) {
	m.Range(func(_ string, item T) bool {
		fn(item)
		return true
	})
}

// CollectStats iterates over all items and applies fn to produce a stats map.
// This replaces the hand-written Stats() boilerplate on every XxxByRoute type.
func CollectStats[T any, S any](m *Manager[T], fn func(T) S) map[string]S {
	result := make(map[string]S)
	m.Range(func(id string, item T) bool {
		result[id] = fn(item)
		return true
	})
	return result
}

// Factory extends Manager with a typed constructor and AddRoute/Stats methods.
// It replaces the boilerplate XxxByRoute wrapper structs that every middleware
// package previously defined (struct + NewXxx + AddRoute + Stats).
//
// Use [NewFactory] for constructors that return (T, error), or
// [SimpleFactory] for constructors that cannot fail.
type Factory[T any, C any] struct {
	Manager[T]
	newFn   func(C) (T, error)
	statsFn func(T) any
	closeFn func(T) // optional per-item cleanup
}

// NewFactory creates a Factory for a constructor that may return an error.
func NewFactory[T any, C any](newFn func(C) (T, error), statsFn func(T) any) *Factory[T, C] {
	return &Factory[T, C]{newFn: newFn, statsFn: statsFn}
}

// SimpleFactory creates a Factory for a constructor that cannot fail.
func SimpleFactory[T any, C any](newFn func(C) T, statsFn func(T) any) *Factory[T, C] {
	return &Factory[T, C]{
		newFn:   func(cfg C) (T, error) { return newFn(cfg), nil },
		statsFn: statsFn,
	}
}

// WithClose sets the per-item cleanup function and returns the Factory for chaining.
// When CloseAll is called, fn is invoked on every stored item.
func (f *Factory[T, C]) WithClose(fn func(T)) *Factory[T, C] {
	f.closeFn = fn
	return f
}

// AddRoute constructs a new item from cfg and stores it for the given route.
func (f *Factory[T, C]) AddRoute(routeID string, cfg C) error {
	item, err := f.newFn(cfg)
	if err != nil {
		return err
	}
	f.Add(routeID, item)
	return nil
}

// Stats returns per-route statistics by applying the stats function to each item.
func (f *Factory[T, C]) Stats() map[string]any {
	if f.statsFn == nil {
		return nil
	}
	return CollectStats(&f.Manager, f.statsFn)
}

// CloseAll calls the close function on every stored item.
// No-op if WithClose was never called.
func (f *Factory[T, C]) CloseAll() {
	if f.closeFn != nil {
		ForEach(&f.Manager, f.closeFn)
	}
}

// NamedFactory is like Factory but passes routeID to the constructor.
// Use this for handlers whose constructor needs the route ID (e.g., for
// logging, metrics labels, or resource namespacing).
type NamedFactory[T any, C any] struct {
	Manager[T]
	newFn   func(routeID string, cfg C) (T, error)
	statsFn func(T) any
	closeFn func(T) // optional per-item cleanup
}

// NewNamedFactory creates a NamedFactory for a constructor that takes (routeID, cfg).
func NewNamedFactory[T any, C any](newFn func(string, C) (T, error), statsFn func(T) any) *NamedFactory[T, C] {
	return &NamedFactory[T, C]{newFn: newFn, statsFn: statsFn}
}

// SimpleNamedFactory creates a NamedFactory for a constructor that cannot fail.
func SimpleNamedFactory[T any, C any](newFn func(string, C) T, statsFn func(T) any) *NamedFactory[T, C] {
	return &NamedFactory[T, C]{
		newFn:   func(id string, cfg C) (T, error) { return newFn(id, cfg), nil },
		statsFn: statsFn,
	}
}

// WithClose sets the per-item cleanup function and returns the NamedFactory for chaining.
func (f *NamedFactory[T, C]) WithClose(fn func(T)) *NamedFactory[T, C] {
	f.closeFn = fn
	return f
}

// AddRoute constructs a new item from routeID+cfg and stores it.
func (f *NamedFactory[T, C]) AddRoute(routeID string, cfg C) error {
	item, err := f.newFn(routeID, cfg)
	if err != nil {
		return err
	}
	f.Add(routeID, item)
	return nil
}

// Stats returns per-route statistics.
func (f *NamedFactory[T, C]) Stats() map[string]any {
	if f.statsFn == nil {
		return nil
	}
	return CollectStats(&f.Manager, f.statsFn)
}

// CloseAll calls the close function on every stored item.
// No-op if WithClose was never called.
func (f *NamedFactory[T, C]) CloseAll() {
	if f.closeFn != nil {
		ForEach(&f.Manager, f.closeFn)
	}
}
