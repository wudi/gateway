package dns

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/registry"
)

// mockResolver implements the resolver interface for testing.
type mockResolver struct {
	mu       sync.Mutex
	srvFunc  func(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	hostFunc func(ctx context.Context, host string) ([]string, error)
	srvCalls int
}

func (m *mockResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	m.mu.Lock()
	m.srvCalls++
	m.mu.Unlock()
	if m.srvFunc != nil {
		return m.srvFunc(ctx, service, proto, name)
	}
	return "", nil, fmt.Errorf("no SRV records")
}

func (m *mockResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	if m.hostFunc != nil {
		return m.hostFunc(ctx, host)
	}
	return nil, fmt.Errorf("no host records")
}

func (m *mockResolver) getSRVCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srvCalls
}

func TestNew(t *testing.T) {
	t.Run("empty domain returns error", func(t *testing.T) {
		_, err := New(config.DNSSRVConfig{})
		if err == nil {
			t.Fatal("expected error for empty domain")
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		r, err := New(config.DNSSRVConfig{Domain: "service.consul"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.protocol != "tcp" {
			t.Errorf("expected protocol tcp, got %s", r.protocol)
		}
		if r.pollInterval != 30*time.Second {
			t.Errorf("expected 30s poll interval, got %v", r.pollInterval)
		}
	})

	t.Run("custom values", func(t *testing.T) {
		r, err := New(config.DNSSRVConfig{
			Domain:       "svc.cluster.local",
			Protocol:     "udp",
			PollInterval: 10 * time.Second,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.protocol != "udp" {
			t.Errorf("expected protocol udp, got %s", r.protocol)
		}
		if r.pollInterval != 10*time.Second {
			t.Errorf("expected 10s poll interval, got %v", r.pollInterval)
		}
		if r.domain != "svc.cluster.local" {
			t.Errorf("expected domain svc.cluster.local, got %s", r.domain)
		}
	})

	t.Run("custom nameserver", func(t *testing.T) {
		r, err := New(config.DNSSRVConfig{
			Domain:     "service.consul",
			Nameserver: "10.0.0.53:8600",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.domain != "service.consul" {
			t.Errorf("expected domain service.consul, got %s", r.domain)
		}
	})
}

func TestRegisterDeregister(t *testing.T) {
	r := &Registry{
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	if err := r.Register(context.Background(), &registry.Service{ID: "test"}); err != nil {
		t.Errorf("Register should be no-op, got error: %v", err)
	}
	if err := r.Deregister(context.Background(), "test"); err != nil {
		t.Errorf("Deregister should be no-op, got error: %v", err)
	}
}

func TestDiscover(t *testing.T) {
	mock := &mockResolver{
		srvFunc: func(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
			return "", []*net.SRV{
				{Target: "node1.example.com.", Port: 8080, Priority: 10, Weight: 60},
				{Target: "node2.example.com.", Port: 8081, Priority: 10, Weight: 40},
				{Target: "node3.example.com.", Port: 9090, Priority: 20, Weight: 100},
			}, nil
		},
		hostFunc: func(_ context.Context, host string) ([]string, error) {
			switch host {
			case "node1.example.com":
				return []string{"192.168.1.1"}, nil
			case "node2.example.com":
				return []string{"192.168.1.2"}, nil
			case "node3.example.com":
				return []string{"192.168.1.3"}, nil
			}
			return nil, fmt.Errorf("unknown host")
		},
	}

	r := &Registry{
		domain:   "example.com",
		protocol: "tcp",
		resolver: mock,
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	services, err := r.Discover(context.Background(), "myservice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(services))
	}

	// Priority 10 should come first (sorted ascending), weight 60 > 40.
	if services[0].Address != "192.168.1.1" {
		t.Errorf("expected first service at 192.168.1.1, got %s", services[0].Address)
	}
	if services[0].Port != 8080 {
		t.Errorf("expected port 8080, got %d", services[0].Port)
	}

	if services[1].Address != "192.168.1.2" {
		t.Errorf("expected second service at 192.168.1.2, got %s", services[1].Address)
	}

	// Priority 20 should come last.
	if services[2].Address != "192.168.1.3" {
		t.Errorf("expected third service at 192.168.1.3, got %s", services[2].Address)
	}

	// Check metadata.
	if services[0].Metadata["srv_priority"] != "10" {
		t.Errorf("expected priority 10, got %s", services[0].Metadata["srv_priority"])
	}
	if services[0].Metadata["srv_weight"] != "60" {
		t.Errorf("expected weight 60, got %s", services[0].Metadata["srv_weight"])
	}
	if services[0].Metadata["srv_target"] != "node1.example.com" {
		t.Errorf("expected target node1.example.com, got %s", services[0].Metadata["srv_target"])
	}

	// Check service ID format.
	if services[0].ID != "myservice-node1.example.com-8080" {
		t.Errorf("unexpected ID: %s", services[0].ID)
	}

	// Health should always be passing.
	for _, svc := range services {
		if svc.Health != registry.HealthPassing {
			t.Errorf("expected HealthPassing, got %s", svc.Health)
		}
		if svc.Name != "myservice" {
			t.Errorf("expected name myservice, got %s", svc.Name)
		}
	}
}

func TestDiscoverCaching(t *testing.T) {
	callCount := 0
	mock := &mockResolver{
		srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
			callCount++
			return "", []*net.SRV{
				{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
			}, nil
		},
		hostFunc: func(_ context.Context, _ string) ([]string, error) {
			return []string{"10.0.0.1"}, nil
		},
	}

	r := &Registry{
		domain:   "example.com",
		protocol: "tcp",
		resolver: mock,
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	// First call fetches.
	_, err := r.Discover(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 SRV call, got %d", callCount)
	}

	// Discover always fetches fresh (cache is fallback on error).
	_, err = r.Discover(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 SRV calls, got %d", callCount)
	}
}

func TestDiscoverFallbackToCache(t *testing.T) {
	calls := 0
	mock := &mockResolver{
		srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
			calls++
			if calls > 1 {
				return "", nil, fmt.Errorf("dns failure")
			}
			return "", []*net.SRV{
				{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
			}, nil
		},
		hostFunc: func(_ context.Context, _ string) ([]string, error) {
			return []string{"10.0.0.1"}, nil
		},
	}

	r := &Registry{
		domain:   "example.com",
		protocol: "tcp",
		resolver: mock,
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	// First call succeeds and caches.
	svc1, err := r.Discover(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(svc1) != 1 {
		t.Fatalf("expected 1 service, got %d", len(svc1))
	}

	// Second call fails but returns cached state.
	svc2, err := r.Discover(context.Background(), "svc")
	if err != nil {
		t.Fatalf("expected cached fallback, got error: %v", err)
	}
	if len(svc2) != 1 {
		t.Fatalf("expected 1 cached service, got %d", len(svc2))
	}
}

func TestDiscoverErrorNoCache(t *testing.T) {
	mock := &mockResolver{
		srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
			return "", nil, fmt.Errorf("dns failure")
		},
	}

	r := &Registry{
		domain:   "example.com",
		protocol: "tcp",
		resolver: mock,
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	_, err := r.Discover(context.Background(), "svc")
	if err == nil {
		t.Fatal("expected error with no cache")
	}
}

func TestDiscoverWithTags(t *testing.T) {
	mock := &mockResolver{
		srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
			return "", []*net.SRV{
				{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
			}, nil
		},
		hostFunc: func(_ context.Context, _ string) ([]string, error) {
			return []string{"10.0.0.1"}, nil
		},
	}

	r := &Registry{
		domain:   "example.com",
		protocol: "tcp",
		resolver: mock,
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	// Tags are ignored for DNS SRV.
	services, err := r.DiscoverWithTags(context.Background(), "svc", []string{"production", "v2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(services) != 1 {
		t.Errorf("expected 1 service, got %d", len(services))
	}
}

func TestResolveTarget(t *testing.T) {
	t.Run("already IP", func(t *testing.T) {
		r := &Registry{
			resolver: &mockResolver{},
		}
		addr := r.resolveTarget(context.Background(), "192.168.1.1")
		if addr != "192.168.1.1" {
			t.Errorf("expected 192.168.1.1, got %s", addr)
		}
	})

	t.Run("hostname to IPv4", func(t *testing.T) {
		r := &Registry{
			resolver: &mockResolver{
				hostFunc: func(_ context.Context, _ string) ([]string, error) {
					return []string{"10.0.0.1"}, nil
				},
			},
		}
		addr := r.resolveTarget(context.Background(), "node1.example.com")
		if addr != "10.0.0.1" {
			t.Errorf("expected 10.0.0.1, got %s", addr)
		}
	})

	t.Run("prefer IPv4 over IPv6", func(t *testing.T) {
		r := &Registry{
			resolver: &mockResolver{
				hostFunc: func(_ context.Context, _ string) ([]string, error) {
					return []string{"::1", "10.0.0.2", "::2"}, nil
				},
			},
		}
		addr := r.resolveTarget(context.Background(), "node1.example.com")
		if addr != "10.0.0.2" {
			t.Errorf("expected 10.0.0.2, got %s", addr)
		}
	})

	t.Run("only IPv6 returned", func(t *testing.T) {
		r := &Registry{
			resolver: &mockResolver{
				hostFunc: func(_ context.Context, _ string) ([]string, error) {
					return []string{"::1"}, nil
				},
			},
		}
		addr := r.resolveTarget(context.Background(), "node1.example.com")
		if addr != "::1" {
			t.Errorf("expected ::1, got %s", addr)
		}
	})

	t.Run("resolution failure fallback", func(t *testing.T) {
		r := &Registry{
			resolver: &mockResolver{
				hostFunc: func(_ context.Context, _ string) ([]string, error) {
					return nil, fmt.Errorf("lookup failed")
				},
			},
		}
		addr := r.resolveTarget(context.Background(), "node1.example.com")
		if addr != "node1.example.com" {
			t.Errorf("expected fallback to hostname, got %s", addr)
		}
	})
}

func TestWatch(t *testing.T) {
	t.Run("initial state sent", func(t *testing.T) {
		mock := &mockResolver{
			srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
				return "", []*net.SRV{
					{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
				}, nil
			},
			hostFunc: func(_ context.Context, _ string) ([]string, error) {
				return []string{"10.0.0.1"}, nil
			},
		}

		r := &Registry{
			domain:       "example.com",
			protocol:     "tcp",
			pollInterval: time.Hour, // long interval so only initial fires
			resolver:     mock,
			cache:        make(map[string][]*registry.Service),
			watchers:     make(map[string]context.CancelFunc),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ch, err := r.Watch(ctx, "svc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		select {
		case services := <-ch:
			if len(services) != 1 {
				t.Errorf("expected 1 service, got %d", len(services))
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for initial state")
		}

		cancel()
	})

	t.Run("change detection triggers update", func(t *testing.T) {
		callCount := 0
		mock := &mockResolver{
			srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
				callCount++
				if callCount <= 2 {
					return "", []*net.SRV{
						{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
					}, nil
				}
				// Return different set on third call.
				return "", []*net.SRV{
					{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
					{Target: "node2.", Port: 9090, Priority: 10, Weight: 50},
				}, nil
			},
			hostFunc: func(_ context.Context, _ string) ([]string, error) {
				return []string{"10.0.0.1"}, nil
			},
		}

		r := &Registry{
			domain:       "example.com",
			protocol:     "tcp",
			pollInterval: 50 * time.Millisecond,
			resolver:     mock,
			cache:        make(map[string][]*registry.Service),
			watchers:     make(map[string]context.CancelFunc),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		ch, err := r.Watch(ctx, "svc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Read initial state.
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("timed out waiting for initial state")
		}

		// Wait for changed state.
		select {
		case services := <-ch:
			if len(services) != 2 {
				t.Errorf("expected 2 services after change, got %d", len(services))
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for change update")
		}

		cancel()
	})

	t.Run("dns failure continues polling", func(t *testing.T) {
		callCount := 0
		mock := &mockResolver{
			srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
				callCount++
				if callCount == 1 {
					return "", []*net.SRV{
						{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
					}, nil
				}
				if callCount <= 3 {
					return "", nil, fmt.Errorf("temporary dns failure")
				}
				return "", []*net.SRV{
					{Target: "node2.", Port: 9090, Priority: 10, Weight: 50},
				}, nil
			},
			hostFunc: func(_ context.Context, _ string) ([]string, error) {
				return []string{"10.0.0.1"}, nil
			},
		}

		r := &Registry{
			domain:       "example.com",
			protocol:     "tcp",
			pollInterval: 50 * time.Millisecond,
			resolver:     mock,
			cache:        make(map[string][]*registry.Service),
			watchers:     make(map[string]context.CancelFunc),
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		ch, err := r.Watch(ctx, "svc")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Read initial state.
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal("timed out waiting for initial state")
		}

		// Should eventually recover and send new state.
		select {
		case services := <-ch:
			if len(services) != 1 || services[0].ID != "svc-node2-9090" {
				t.Errorf("expected recovered service node2:9090, got %v", services)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for recovery")
		}

		cancel()
	})
}

func TestClose(t *testing.T) {
	r := &Registry{
		domain:       "example.com",
		protocol:     "tcp",
		pollInterval: 50 * time.Millisecond,
		resolver: &mockResolver{
			srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
				return "", []*net.SRV{
					{Target: "node1.", Port: 8080, Priority: 10, Weight: 50},
				}, nil
			},
			hostFunc: func(_ context.Context, _ string) ([]string, error) {
				return []string{"10.0.0.1"}, nil
			},
		},
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	ctx := context.Background()
	ch, err := r.Watch(ctx, "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read initial state.
	<-ch

	if err := r.Close(); err != nil {
		t.Fatalf("unexpected error from Close: %v", err)
	}

	// Channel should eventually close.
	select {
	case _, ok := <-ch:
		if ok {
			// Got a final message before close, drain it.
			select {
			case _, ok := <-ch:
				if ok {
					t.Error("expected channel to close after Close()")
				}
			case <-time.After(2 * time.Second):
				t.Error("timed out waiting for channel close")
			}
		}
	case <-time.After(2 * time.Second):
		t.Error("timed out waiting for channel close")
	}
}

func TestServicesEqual(t *testing.T) {
	tests := []struct {
		name  string
		a, b  []*registry.Service
		equal bool
	}{
		{
			name:  "both nil",
			a:     nil,
			b:     nil,
			equal: true,
		},
		{
			name:  "different lengths",
			a:     []*registry.Service{{ID: "a"}},
			b:     []*registry.Service{{ID: "a"}, {ID: "b"}},
			equal: false,
		},
		{
			name:  "same IDs same order",
			a:     []*registry.Service{{ID: "a"}, {ID: "b"}},
			b:     []*registry.Service{{ID: "a"}, {ID: "b"}},
			equal: true,
		},
		{
			name:  "same IDs different order",
			a:     []*registry.Service{{ID: "b"}, {ID: "a"}},
			b:     []*registry.Service{{ID: "a"}, {ID: "b"}},
			equal: true,
		},
		{
			name:  "different IDs",
			a:     []*registry.Service{{ID: "a"}, {ID: "b"}},
			b:     []*registry.Service{{ID: "a"}, {ID: "c"}},
			equal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := servicesEqual(tt.a, tt.b); got != tt.equal {
				t.Errorf("servicesEqual() = %v, want %v", got, tt.equal)
			}
		})
	}
}

func TestSortOrder(t *testing.T) {
	mock := &mockResolver{
		srvFunc: func(_ context.Context, _, _, _ string) (string, []*net.SRV, error) {
			return "", []*net.SRV{
				{Target: "high-pri.", Port: 8080, Priority: 20, Weight: 100},
				{Target: "low-pri-low-w.", Port: 8081, Priority: 10, Weight: 30},
				{Target: "low-pri-high-w.", Port: 8082, Priority: 10, Weight: 70},
			}, nil
		},
		hostFunc: func(_ context.Context, host string) ([]string, error) {
			return []string{"10.0.0.1"}, nil
		},
	}

	r := &Registry{
		domain:   "example.com",
		protocol: "tcp",
		resolver: mock,
		cache:    make(map[string][]*registry.Service),
		watchers: make(map[string]context.CancelFunc),
	}

	services, err := r.Discover(context.Background(), "svc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Priority 10, weight 70 first.
	if services[0].Metadata["srv_target"] != "low-pri-high-w" {
		t.Errorf("expected low-pri-high-w first, got %s", services[0].Metadata["srv_target"])
	}
	// Priority 10, weight 30 second.
	if services[1].Metadata["srv_target"] != "low-pri-low-w" {
		t.Errorf("expected low-pri-low-w second, got %s", services[1].Metadata["srv_target"])
	}
	// Priority 20 last.
	if services[2].Metadata["srv_target"] != "high-pri" {
		t.Errorf("expected high-pri last, got %s", services[2].Metadata["srv_target"])
	}
}
