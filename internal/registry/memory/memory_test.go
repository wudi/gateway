package memory

import (
	"context"
	"testing"
	"time"

	"github.com/wudi/gateway/internal/registry"
)

func TestMemoryRegistry(t *testing.T) {
	r := New()
	ctx := context.Background()

	// Register a service
	svc := &registry.Service{
		ID:      "svc-1",
		Name:    "test-service",
		Address: "127.0.0.1",
		Port:    8080,
		Tags:    []string{"production"},
		Health:  registry.HealthPassing,
	}

	err := r.Register(ctx, svc)
	if err != nil {
		t.Fatalf("failed to register service: %v", err)
	}

	// Discover the service
	services, err := r.Discover(ctx, "test-service")
	if err != nil {
		t.Fatalf("failed to discover services: %v", err)
	}

	if len(services) != 1 {
		t.Errorf("expected 1 service, got %d", len(services))
	}

	if services[0].ID != "svc-1" {
		t.Errorf("expected service ID 'svc-1', got '%s'", services[0].ID)
	}
}

func TestMemoryRegistryDeregister(t *testing.T) {
	r := New()
	ctx := context.Background()

	svc := &registry.Service{
		ID:      "svc-1",
		Name:    "test-service",
		Address: "127.0.0.1",
		Port:    8080,
	}

	r.Register(ctx, svc)

	// Deregister
	err := r.Deregister(ctx, "svc-1")
	if err != nil {
		t.Fatalf("failed to deregister service: %v", err)
	}

	// Should not find the service
	services, _ := r.Discover(ctx, "test-service")
	if len(services) != 0 {
		t.Errorf("expected 0 services, got %d", len(services))
	}
}

func TestMemoryRegistryDiscoverWithTags(t *testing.T) {
	r := New()
	ctx := context.Background()

	// Register services with different tags
	r.Register(ctx, &registry.Service{
		ID:      "svc-1",
		Name:    "test-service",
		Address: "127.0.0.1",
		Port:    8080,
		Tags:    []string{"production", "v1"},
	})

	r.Register(ctx, &registry.Service{
		ID:      "svc-2",
		Name:    "test-service",
		Address: "127.0.0.2",
		Port:    8080,
		Tags:    []string{"staging", "v1"},
	})

	r.Register(ctx, &registry.Service{
		ID:      "svc-3",
		Name:    "test-service",
		Address: "127.0.0.3",
		Port:    8080,
		Tags:    []string{"production", "v2"},
	})

	// Filter by production tag
	services, _ := r.DiscoverWithTags(ctx, "test-service", []string{"production"})
	if len(services) != 2 {
		t.Errorf("expected 2 production services, got %d", len(services))
	}

	// Filter by production + v1
	services, _ = r.DiscoverWithTags(ctx, "test-service", []string{"production", "v1"})
	if len(services) != 1 {
		t.Errorf("expected 1 production+v1 service, got %d", len(services))
	}
}

func TestMemoryRegistryWatch(t *testing.T) {
	r := New()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start watching
	ch, err := r.Watch(ctx, "test-service")
	if err != nil {
		t.Fatalf("failed to watch: %v", err)
	}

	// Register a service
	svc := &registry.Service{
		ID:      "svc-1",
		Name:    "test-service",
		Address: "127.0.0.1",
		Port:    8080,
	}

	// Wait for initial state
	select {
	case services := <-ch:
		if len(services) != 0 {
			t.Errorf("expected 0 initial services, got %d", len(services))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial state")
	}

	// Register and wait for update
	r.Register(ctx, svc)

	select {
	case services := <-ch:
		if len(services) != 1 {
			t.Errorf("expected 1 service after register, got %d", len(services))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for watch update")
	}
}

func TestMemoryRegistryUnhealthyFilter(t *testing.T) {
	r := New()
	ctx := context.Background()

	// Register healthy service
	r.Register(ctx, &registry.Service{
		ID:      "svc-1",
		Name:    "test-service",
		Address: "127.0.0.1",
		Port:    8080,
		Health:  registry.HealthPassing,
	})

	// Register unhealthy service
	r.Register(ctx, &registry.Service{
		ID:      "svc-2",
		Name:    "test-service",
		Address: "127.0.0.2",
		Port:    8080,
		Health:  registry.HealthCritical,
	})

	// Discover should only return healthy
	services, _ := r.Discover(ctx, "test-service")
	if len(services) != 1 {
		t.Errorf("expected 1 healthy service, got %d", len(services))
	}

	if services[0].ID != "svc-1" {
		t.Errorf("expected healthy service svc-1, got %s", services[0].ID)
	}
}

func TestMemoryRegistryAutoID(t *testing.T) {
	r := New()
	ctx := context.Background()

	// Register without ID
	svc := &registry.Service{
		Name:    "test-service",
		Address: "127.0.0.1",
		Port:    8080,
	}

	err := r.Register(ctx, svc)
	if err != nil {
		t.Fatalf("failed to register service: %v", err)
	}

	// Should have auto-generated ID
	if svc.ID == "" {
		t.Error("expected auto-generated ID")
	}

	// Should be discoverable
	services, _ := r.Discover(ctx, "test-service")
	if len(services) != 1 {
		t.Errorf("expected 1 service, got %d", len(services))
	}
}

func TestServiceURL(t *testing.T) {
	svc := &registry.Service{
		Address: "192.168.1.100",
		Port:    8080,
	}

	expected := "http://192.168.1.100:8080"
	if svc.URL() != expected {
		t.Errorf("expected URL %s, got %s", expected, svc.URL())
	}
}
