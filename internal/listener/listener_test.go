package listener

import (
	"context"
	"testing"
	"time"
)

// mockListener is a simple implementation of Listener for testing
type mockListener struct {
	id       string
	protocol string
	addr     string
	started  bool
	stopped  bool
}

func (m *mockListener) ID() string       { return m.id }
func (m *mockListener) Protocol() string { return m.protocol }
func (m *mockListener) Addr() string     { return m.addr }
func (m *mockListener) Start(ctx context.Context) error {
	m.started = true
	return nil
}
func (m *mockListener) Stop(ctx context.Context) error {
	m.stopped = true
	return nil
}

func TestManagerAdd(t *testing.T) {
	m := NewManager()

	l := &mockListener{id: "test1", protocol: "http", addr: ":8080"}
	err := m.Add(l)
	if err != nil {
		t.Errorf("Add failed: %v", err)
	}

	// Adding duplicate should fail
	err = m.Add(l)
	if err == nil {
		t.Error("Add should fail for duplicate listener ID")
	}
}

func TestManagerGet(t *testing.T) {
	m := NewManager()

	l := &mockListener{id: "test1", protocol: "http", addr: ":8080"}
	m.Add(l)

	// Get existing listener
	got, ok := m.Get("test1")
	if !ok {
		t.Error("Get should return true for existing listener")
	}
	if got.ID() != "test1" {
		t.Errorf("Got wrong listener ID: %s", got.ID())
	}

	// Get non-existent listener
	_, ok = m.Get("nonexistent")
	if ok {
		t.Error("Get should return false for non-existent listener")
	}
}

func TestManagerRemove(t *testing.T) {
	m := NewManager()

	l := &mockListener{id: "test1", protocol: "http", addr: ":8080"}
	m.Add(l)

	// Remove existing listener
	err := m.Remove("test1")
	if err != nil {
		t.Errorf("Remove failed: %v", err)
	}

	// Verify it's gone
	_, ok := m.Get("test1")
	if ok {
		t.Error("Listener should not exist after removal")
	}

	// Remove non-existent listener
	err = m.Remove("nonexistent")
	if err == nil {
		t.Error("Remove should fail for non-existent listener")
	}
}

func TestManagerCount(t *testing.T) {
	m := NewManager()

	if m.Count() != 0 {
		t.Errorf("Initial count should be 0, got %d", m.Count())
	}

	m.Add(&mockListener{id: "l1"})
	m.Add(&mockListener{id: "l2"})
	m.Add(&mockListener{id: "l3"})

	if m.Count() != 3 {
		t.Errorf("Count should be 3, got %d", m.Count())
	}

	m.Remove("l2")
	if m.Count() != 2 {
		t.Errorf("Count should be 2 after removal, got %d", m.Count())
	}
}

func TestManagerList(t *testing.T) {
	m := NewManager()

	m.Add(&mockListener{id: "l1"})
	m.Add(&mockListener{id: "l2"})

	ids := m.List()
	if len(ids) != 2 {
		t.Errorf("List should return 2 IDs, got %d", len(ids))
	}

	// Check that both IDs are present
	idMap := make(map[string]bool)
	for _, id := range ids {
		idMap[id] = true
	}
	if !idMap["l1"] || !idMap["l2"] {
		t.Error("List should contain both listener IDs")
	}
}

func TestManagerStartAll(t *testing.T) {
	m := NewManager()

	l1 := &mockListener{id: "l1"}
	l2 := &mockListener{id: "l2"}
	m.Add(l1)
	m.Add(l2)

	ctx := context.Background()
	err := m.StartAll(ctx)
	if err != nil {
		t.Errorf("StartAll failed: %v", err)
	}

	// Give goroutines time to start
	time.Sleep(50 * time.Millisecond)

	if !l1.started || !l2.started {
		t.Error("All listeners should be started")
	}
}

func TestManagerStopAll(t *testing.T) {
	m := NewManager()

	l1 := &mockListener{id: "l1"}
	l2 := &mockListener{id: "l2"}
	m.Add(l1)
	m.Add(l2)

	ctx := context.Background()
	err := m.StopAll(ctx)
	if err != nil {
		t.Errorf("StopAll failed: %v", err)
	}

	if !l1.stopped || !l2.stopped {
		t.Error("All listeners should be stopped")
	}
}
