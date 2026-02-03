package listener

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// Listener represents a network listener that can accept connections
type Listener interface {
	// ID returns the unique identifier for this listener
	ID() string

	// Protocol returns the protocol type (http, tcp, udp)
	Protocol() string

	// Start starts the listener and begins accepting connections
	Start(ctx context.Context) error

	// Stop gracefully stops the listener
	Stop(ctx context.Context) error

	// Addr returns the address the listener is bound to
	Addr() string
}

// Manager manages multiple listeners
type Manager struct {
	listeners map[string]Listener
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewManager creates a new listener manager
func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		listeners: make(map[string]Listener),
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Add adds a listener to the manager
func (m *Manager) Add(l Listener) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.listeners[l.ID()]; exists {
		return fmt.Errorf("listener with id %s already exists", l.ID())
	}

	m.listeners[l.ID()] = l
	return nil
}

// Get returns a listener by ID
func (m *Manager) Get(id string) (Listener, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.listeners[id]
	return l, ok
}

// Remove removes a listener by ID
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.listeners[id]; !exists {
		return fmt.Errorf("listener with id %s not found", id)
	}

	delete(m.listeners, id)
	return nil
}

// StartAll starts all registered listeners
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	errCh := make(chan error, len(m.listeners))

	for _, l := range m.listeners {
		listener := l // capture for goroutine
		go func() {
			log.Printf("Starting %s listener %s on %s", listener.Protocol(), listener.ID(), listener.Addr())
			if err := listener.Start(ctx); err != nil {
				errCh <- fmt.Errorf("listener %s: %w", listener.ID(), err)
			}
		}()
	}

	// Check for immediate errors (non-blocking)
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

// StopAll gracefully stops all listeners
func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	m.cancel()

	var wg sync.WaitGroup
	errCh := make(chan error, len(m.listeners))

	for _, l := range m.listeners {
		wg.Add(1)
		listener := l // capture for goroutine
		go func() {
			defer wg.Done()
			log.Printf("Stopping %s listener %s", listener.Protocol(), listener.ID())
			if err := listener.Stop(ctx); err != nil {
				errCh <- fmt.Errorf("listener %s: %w", listener.ID(), err)
			}
		}()
	}

	wg.Wait()
	close(errCh)

	// Collect any errors
	var errs []error
	for err := range errCh {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors stopping listeners: %v", errs)
	}

	return nil
}

// Count returns the number of registered listeners
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.listeners)
}

// List returns all listener IDs
func (m *Manager) List() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.listeners))
	for id := range m.listeners {
		ids = append(ids, id)
	}
	return ids
}
