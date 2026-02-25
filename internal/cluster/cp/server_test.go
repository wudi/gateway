package cp

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

func newTestServer() *Server {
	return &Server{
		nodes:             make(map[string]*connectedNode),
		broadcast:         make(chan struct{}),
		heartbeatInterval: 10 * time.Second,
		logger:            zap.NewNop(),
	}
}

func TestCompatibleVersion(t *testing.T) {
	tests := []struct {
		cp, dp string
		want   bool
	}{
		{"1.4.0", "1.4.0", true},
		{"1.4.0", "1.4.1", true},
		{"1.4.0", "1.5.0", false},
		{"1.4.0", "2.4.0", false},
		{"v1.4.0", "1.4.2", true},
		{"1.4", "1.4.0", true},
		{"1", "1", true},
		{"1", "2", false},
	}
	for _, tt := range tests {
		t.Run(tt.cp+"_vs_"+tt.dp, func(t *testing.T) {
			if got := compatibleVersion(tt.cp, tt.dp); got != tt.want {
				t.Errorf("compatibleVersion(%q, %q) = %v, want %v", tt.cp, tt.dp, got, tt.want)
			}
		})
	}
}

func TestMajorMinor(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"1.4.0", "1.4"},
		{"v1.4.0", "1.4"},
		{"1.4", "1.4"},
		{"1", "1"},
		{"2.0.0-rc1", "2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := majorMinor(tt.input); got != tt.want {
				t.Errorf("majorMinor(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPushConfigWakesStreams(t *testing.T) {
	s := newTestServer()

	// Capture broadcast channel before push
	s.mu.RLock()
	bcast := s.broadcast
	s.mu.RUnlock()

	// Push config
	s.PushConfig(ConfigEnvelope{
		YAML:      []byte("test: true"),
		Hash:      12345,
		Timestamp: time.Now(),
		Source:    "test",
	})

	// Old broadcast channel should be closed (non-blocking read)
	select {
	case <-bcast:
		// Good — channel was closed
	default:
		t.Fatal("broadcast channel was not closed after PushConfig")
	}

	// Version should be set
	s.mu.RLock()
	if s.current.Version != 1 {
		t.Errorf("expected version 1, got %d", s.current.Version)
	}
	s.mu.RUnlock()
}

func TestConnectedNodesTracking(t *testing.T) {
	s := newTestServer()

	// Add nodes manually (normally done by ConfigStream)
	s.mu.Lock()
	s.nodes["node-1"] = &connectedNode{
		ConnectedNode: ConnectedNode{
			NodeID:        "node-1",
			Hostname:      "host-1",
			Version:       "1.0.0",
			LastHeartbeat: time.Now(),
			Status:        "connected",
		},
	}
	s.nodes["node-2"] = &connectedNode{
		ConnectedNode: ConnectedNode{
			NodeID:        "node-2",
			Hostname:      "host-2",
			Version:       "1.0.0",
			LastHeartbeat: time.Now(),
			Status:        "connected",
		},
	}
	s.mu.Unlock()

	nodes := s.ConnectedNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	// Remove one
	s.removeNode("node-1")
	nodes = s.ConnectedNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after remove, got %d", len(nodes))
	}
	if nodes[0].NodeID != "node-2" {
		t.Errorf("expected node-2, got %s", nodes[0].NodeID)
	}
}

func TestStaleNodeDetection(t *testing.T) {
	s := newTestServer()
	s.heartbeatInterval = 1 * time.Millisecond // very short for test

	s.mu.Lock()
	s.nodes["stale-node"] = &connectedNode{
		ConnectedNode: ConnectedNode{
			NodeID:        "stale-node",
			LastHeartbeat: time.Now().Add(-1 * time.Hour), // long ago
			Status:        "connected",
		},
	}
	s.mu.Unlock()

	// Simulate stale cleanup
	s.mu.Lock()
	staleThreshold := 3 * s.heartbeatInterval
	for _, node := range s.nodes {
		if time.Since(node.LastHeartbeat) > staleThreshold {
			node.Status = "stale"
		}
	}
	s.mu.Unlock()

	nodes := s.ConnectedNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].Status != "stale" {
		t.Errorf("expected stale status, got %q", nodes[0].Status)
	}
}

func TestPushConfigIncrementsVersion(t *testing.T) {
	s := newTestServer()

	s.PushConfig(ConfigEnvelope{YAML: []byte("v1"), Hash: 1, Source: "test"})
	if s.current.Version != 1 {
		t.Errorf("expected version 1, got %d", s.current.Version)
	}

	s.PushConfig(ConfigEnvelope{YAML: []byte("v2"), Hash: 2, Source: "test"})
	if s.current.Version != 2 {
		t.Errorf("expected version 2, got %d", s.current.Version)
	}

	// Explicit version overrides auto-increment
	s.PushConfig(ConfigEnvelope{Version: 10, YAML: []byte("v10"), Hash: 10, Source: "test"})
	if s.current.Version != 10 {
		t.Errorf("expected version 10, got %d", s.current.Version)
	}
}

func TestCurrentConfig(t *testing.T) {
	s := newTestServer()

	// Initially empty
	env := s.CurrentConfig()
	if env.Version != 0 || len(env.YAML) != 0 {
		t.Error("expected empty config initially")
	}

	// After push
	s.PushConfig(ConfigEnvelope{
		YAML:      []byte("routes: []"),
		Hash:      42,
		Timestamp: time.Now(),
		Source:    "file",
	})

	env = s.CurrentConfig()
	if env.Version != 1 {
		t.Errorf("expected version 1, got %d", env.Version)
	}
	if string(env.YAML) != "routes: []" {
		t.Errorf("unexpected YAML: %s", env.YAML)
	}
	if env.Hash != 42 {
		t.Errorf("expected hash 42, got %d", env.Hash)
	}
	if env.Source != "file" {
		t.Errorf("expected source 'file', got %q", env.Source)
	}
}
