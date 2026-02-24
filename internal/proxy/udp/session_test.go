package udp

import (
	"testing"
	"time"

	"github.com/wudi/gateway/config"
)

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager(SessionManagerConfig{
		SessionTimeout:  100 * time.Millisecond,
		CleanupInterval: 50 * time.Millisecond,
	})
	defer sm.Close()

	// Test initial count
	if sm.Count() != 0 {
		t.Errorf("Initial count should be 0, got %d", sm.Count())
	}
}

func TestSessionManagerGetNonExistent(t *testing.T) {
	sm := NewSessionManager(SessionManagerConfig{})
	defer sm.Close()

	_, exists := sm.Get("192.168.1.1:12345")
	if exists {
		t.Error("Get should return false for non-existent session")
	}
}

func TestSessionUpdateLastActive(t *testing.T) {
	session := &Session{
		LastActive: time.Now().Add(-1 * time.Hour),
	}

	oldTime := session.GetLastActive()
	time.Sleep(10 * time.Millisecond)
	session.UpdateLastActive()
	newTime := session.GetLastActive()

	if !newTime.After(oldTime) {
		t.Error("UpdateLastActive should update the timestamp")
	}
}

func TestParseUDPBackendURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
		wantErr  bool
	}{
		{"udp://8.8.8.8:53", "8.8.8.8:53", false},
		{"udp://dns-server:53", "dns-server:53", false},
		{"8.8.8.8:53", "8.8.8.8:53", false},
		{"192.168.1.1:5353", "192.168.1.1:5353", false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseUDPBackendURL(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseUDPBackendURL(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("parseUDPBackendURL(%q) unexpected error: %v", tt.input, err)
				return
			}
			if result != tt.expected {
				t.Errorf("parseUDPBackendURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSessionManagerStats(t *testing.T) {
	sm := NewSessionManager(SessionManagerConfig{
		SessionTimeout: 10 * time.Second,
	})
	defer sm.Close()

	// Stats should be empty initially
	stats := sm.Stats()
	if len(stats) != 0 {
		t.Errorf("Initial stats should be empty, got %d entries", len(stats))
	}
}

func TestSessionManagerClose(t *testing.T) {
	sm := NewSessionManager(SessionManagerConfig{})

	// Close should not panic
	err := sm.Close()
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}

	// Count after close should be 0
	if sm.Count() != 0 {
		t.Errorf("Count after close should be 0, got %d", sm.Count())
	}
}

func TestSessionManagerRemove(t *testing.T) {
	sm := NewSessionManager(SessionManagerConfig{
		SessionTimeout: 10 * time.Second,
	})
	defer sm.Close()

	// Remove non-existent session should not panic
	sm.Remove("192.168.1.1:12345")

	if sm.Count() != 0 {
		t.Errorf("Count should be 0 after removing non-existent session")
	}
}

func TestUDPProxyAddRoute(t *testing.T) {
	proxy := NewProxy(Config{
		SessionTimeout: 30 * time.Second,
	})
	defer proxy.Close()

	// Add route
	routeCfg := config.UDPRouteConfig{
		ID:       "test-udp",
		Listener: "udp-test",
		Backends: []config.BackendConfig{
			{URL: "127.0.0.1:5353"},
		},
	}

	err := proxy.AddRoute(routeCfg)
	if err != nil {
		t.Fatalf("AddRoute failed: %v", err)
	}

	// Verify route was added
	route := proxy.GetRoute("test-udp")
	if route == nil {
		t.Error("Route should exist after AddRoute")
	}

	// Remove route
	proxy.RemoveRoute("test-udp")
	route = proxy.GetRoute("test-udp")
	if route != nil {
		t.Error("Route should not exist after RemoveRoute")
	}
}

func TestUDPProxyGetRoutesForListener(t *testing.T) {
	proxy := NewProxy(Config{})
	defer proxy.Close()

	// Add routes for different listeners
	proxy.AddRoute(config.UDPRouteConfig{
		ID:       "route1",
		Listener: "listener-a",
		Backends: []config.BackendConfig{{URL: "127.0.0.1:5353"}},
	})
	proxy.AddRoute(config.UDPRouteConfig{
		ID:       "route2",
		Listener: "listener-b",
		Backends: []config.BackendConfig{{URL: "127.0.0.1:5354"}},
	})
	proxy.AddRoute(config.UDPRouteConfig{
		ID:       "route3",
		Listener: "listener-a",
		Backends: []config.BackendConfig{{URL: "127.0.0.1:5355"}},
	})

	// Get routes for listener-a
	routes := proxy.GetRoutesForListener("listener-a")
	if len(routes) != 2 {
		t.Errorf("Expected 2 routes for listener-a, got %d", len(routes))
	}

	// Get routes for listener-b
	routes = proxy.GetRoutesForListener("listener-b")
	if len(routes) != 1 {
		t.Errorf("Expected 1 route for listener-b, got %d", len(routes))
	}

	// Get routes for non-existent listener
	routes = proxy.GetRoutesForListener("listener-c")
	if len(routes) != 0 {
		t.Errorf("Expected 0 routes for non-existent listener, got %d", len(routes))
	}
}

func TestUDPProxySessionCount(t *testing.T) {
	proxy := NewProxy(Config{})
	defer proxy.Close()

	// Initial session count should be 0
	if proxy.SessionCount() != 0 {
		t.Errorf("Initial session count should be 0, got %d", proxy.SessionCount())
	}
}
