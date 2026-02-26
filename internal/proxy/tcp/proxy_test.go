package tcp

import (
	"testing"
	"time"

	"github.com/wudi/runway/config"
)

func TestTCPProxyAddRoute(t *testing.T) {
	proxy := NewProxy(Config{
		ConnectTimeout: 10 * time.Second,
		IdleTimeout:    5 * time.Minute,
	})
	defer proxy.Close()

	// Add route
	routeCfg := config.TCPRouteConfig{
		ID:       "test-tcp",
		Listener: "tcp-test",
		Match: config.TCPMatchConfig{
			SNI: []string{"example.com"},
		},
		Backends: []config.BackendConfig{
			{URL: "tcp://127.0.0.1:3306"},
		},
	}

	err := proxy.AddRoute(routeCfg)
	if err != nil {
		t.Fatalf("AddRoute failed: %v", err)
	}

	// Verify route was added
	route := proxy.GetRoute("test-tcp")
	if route == nil {
		t.Error("Route should exist after AddRoute")
	}

	// Remove route
	proxy.RemoveRoute("test-tcp")
	route = proxy.GetRoute("test-tcp")
	if route != nil {
		t.Error("Route should not exist after RemoveRoute")
	}
}

func TestTCPProxyGetRoutesForListener(t *testing.T) {
	proxy := NewProxy(Config{})
	defer proxy.Close()

	// Add routes for different listeners
	proxy.AddRoute(config.TCPRouteConfig{
		ID:       "route1",
		Listener: "listener-a",
		Backends: []config.BackendConfig{{URL: "127.0.0.1:3306"}},
	})
	proxy.AddRoute(config.TCPRouteConfig{
		ID:       "route2",
		Listener: "listener-b",
		Backends: []config.BackendConfig{{URL: "127.0.0.1:3307"}},
	})
	proxy.AddRoute(config.TCPRouteConfig{
		ID:       "route3",
		Listener: "listener-a",
		Backends: []config.BackendConfig{{URL: "127.0.0.1:3308"}},
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

func TestTCPProxyRouteWithCIDR(t *testing.T) {
	proxy := NewProxy(Config{})
	defer proxy.Close()

	// Add route with CIDR matching
	routeCfg := config.TCPRouteConfig{
		ID:       "cidr-route",
		Listener: "tcp-test",
		Match: config.TCPMatchConfig{
			SourceCIDR: []string{"192.168.1.0/24", "10.0.0.0/8"},
		},
		Backends: []config.BackendConfig{
			{URL: "127.0.0.1:3306"},
		},
	}

	err := proxy.AddRoute(routeCfg)
	if err != nil {
		t.Fatalf("AddRoute with CIDR failed: %v", err)
	}

	route := proxy.GetRoute("cidr-route")
	if route == nil {
		t.Fatal("Route should exist")
	}

	if len(route.CIDRs) != 2 {
		t.Errorf("Expected 2 CIDRs, got %d", len(route.CIDRs))
	}
}

func TestTCPProxyRouteWithInvalidCIDR(t *testing.T) {
	proxy := NewProxy(Config{})
	defer proxy.Close()

	// Add route with invalid CIDR
	routeCfg := config.TCPRouteConfig{
		ID:       "invalid-cidr-route",
		Listener: "tcp-test",
		Match: config.TCPMatchConfig{
			SourceCIDR: []string{"invalid-cidr"},
		},
		Backends: []config.BackendConfig{
			{URL: "127.0.0.1:3306"},
		},
	}

	err := proxy.AddRoute(routeCfg)
	if err == nil {
		t.Error("AddRoute should fail with invalid CIDR")
	}
}

func TestTCPProxyRouteWithInvalidBackendURL(t *testing.T) {
	proxy := NewProxy(Config{})
	defer proxy.Close()

	// Add route with invalid backend URL
	routeCfg := config.TCPRouteConfig{
		ID:       "invalid-backend-route",
		Listener: "tcp-test",
		Backends: []config.BackendConfig{
			{URL: "invalid-url"},
		},
	}

	err := proxy.AddRoute(routeCfg)
	if err == nil {
		t.Error("AddRoute should fail with invalid backend URL")
	}
}

func TestTCPProxyClose(t *testing.T) {
	proxy := NewProxy(Config{})

	// Close should not panic
	err := proxy.Close()
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestConnPool(t *testing.T) {
	pool := NewConnPool(ConnPoolConfig{
		MaxIdle:     5,
		MaxIdleTime: 1 * time.Minute,
		MaxLifetime: 5 * time.Minute,
		DialTimeout: 5 * time.Second,
	})
	defer pool.Close()

	// Stats should be empty initially
	stats := pool.Stats()
	if len(stats) != 0 {
		t.Errorf("Initial pool stats should be empty, got %d entries", len(stats))
	}
}

func TestConnPoolClose(t *testing.T) {
	pool := NewConnPool(ConnPoolConfig{})

	// Close should not panic
	err := pool.Close()
	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}
