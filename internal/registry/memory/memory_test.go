package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wudi/runway/internal/registry"
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

func TestGetAll(t *testing.T) {
	r := New()
	ctx := context.Background()

	r.Register(ctx, &registry.Service{ID: "s1", Name: "svc-a", Address: "127.0.0.1", Port: 8080})
	r.Register(ctx, &registry.Service{ID: "s2", Name: "svc-b", Address: "127.0.0.2", Port: 8080})
	r.Register(ctx, &registry.Service{ID: "s3", Name: "svc-a", Address: "127.0.0.3", Port: 8080, Health: registry.HealthCritical})

	all := r.GetAll()
	if len(all) != 3 {
		t.Errorf("expected 3 services, got %d", len(all))
	}
}

func TestCloseWithoutAPI(t *testing.T) {
	r := New()
	if err := r.Close(); err != nil {
		t.Errorf("Close without API should not error: %v", err)
	}
}

func TestHandleServicesGET(t *testing.T) {
	r := New()
	ctx := context.Background()
	r.Register(ctx, &registry.Service{ID: "s1", Name: "web", Address: "127.0.0.1", Port: 8080})
	r.Register(ctx, &registry.Service{ID: "s2", Name: "api", Address: "127.0.0.2", Port: 8080})

	// List all
	req := httptest.NewRequest("GET", "/services", nil)
	w := httptest.NewRecorder()
	r.handleServices(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var services []*registry.Service
	json.NewDecoder(w.Body).Decode(&services)
	if len(services) != 2 {
		t.Errorf("expected 2 services, got %d", len(services))
	}

	// Filter by name
	req = httptest.NewRequest("GET", "/services?name=web", nil)
	w = httptest.NewRecorder()
	r.handleServices(w, req)

	services = nil
	json.NewDecoder(w.Body).Decode(&services)
	if len(services) != 1 {
		t.Errorf("expected 1 service with name=web, got %d", len(services))
	}
}

func TestHandleServicesPOST(t *testing.T) {
	r := New()

	body := `{"name":"web","address":"127.0.0.1","port":8080}`
	req := httptest.NewRequest("POST", "/services", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.handleServices(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", w.Code)
	}

	var svc registry.Service
	json.NewDecoder(w.Body).Decode(&svc)
	if svc.Name != "web" {
		t.Errorf("name = %q, want %q", svc.Name, "web")
	}
	if svc.ID == "" {
		t.Error("ID should be auto-generated")
	}
}

func TestHandleServicesPOSTInvalidJSON(t *testing.T) {
	r := New()
	req := httptest.NewRequest("POST", "/services", strings.NewReader("{invalid"))
	w := httptest.NewRecorder()
	r.handleServices(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleServicesPOSTMissingFields(t *testing.T) {
	r := New()

	tests := []struct {
		name string
		body string
	}{
		{"missing name", `{"address":"127.0.0.1","port":8080}`},
		{"missing address", `{"name":"web","port":8080}`},
		{"missing port", `{"name":"web","address":"127.0.0.1"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/services", strings.NewReader(tt.body))
			w := httptest.NewRecorder()
			r.handleServices(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", w.Code)
			}
		})
	}
}

func TestHandleServicesMethodNotAllowed(t *testing.T) {
	r := New()
	req := httptest.NewRequest("DELETE", "/services", nil)
	w := httptest.NewRecorder()
	r.handleServices(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleServiceGET(t *testing.T) {
	r := New()
	ctx := context.Background()
	r.Register(ctx, &registry.Service{ID: "s1", Name: "web", Address: "127.0.0.1", Port: 8080})

	// Found
	req := httptest.NewRequest("GET", "/services/s1", nil)
	w := httptest.NewRecorder()
	r.handleService(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	// Not found
	req = httptest.NewRequest("GET", "/services/notfound", nil)
	w = httptest.NewRecorder()
	r.handleService(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleServicePUT(t *testing.T) {
	r := New()
	ctx := context.Background()
	r.Register(ctx, &registry.Service{ID: "s1", Name: "web", Address: "127.0.0.1", Port: 8080})

	body := `{"name":"web-updated","address":"127.0.0.2","port":9090}`
	req := httptest.NewRequest("PUT", "/services/s1", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.handleService(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}

	var svc registry.Service
	json.NewDecoder(w.Body).Decode(&svc)
	if svc.Name != "web-updated" {
		t.Errorf("name = %q, want %q", svc.Name, "web-updated")
	}
	if svc.ID != "s1" {
		t.Errorf("ID = %q, want %q", svc.ID, "s1")
	}
}

func TestHandleServiceDELETE(t *testing.T) {
	r := New()
	ctx := context.Background()
	r.Register(ctx, &registry.Service{ID: "s1", Name: "web", Address: "127.0.0.1", Port: 8080})

	// Delete existing
	req := httptest.NewRequest("DELETE", "/services/s1", nil)
	w := httptest.NewRecorder()
	r.handleService(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}

	// Delete non-existent
	req = httptest.NewRequest("DELETE", "/services/s1", nil)
	w = httptest.NewRecorder()
	r.handleService(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestHandleServiceMethodNotAllowed(t *testing.T) {
	r := New()
	req := httptest.NewRequest("PATCH", "/services/s1", nil)
	w := httptest.NewRecorder()
	r.handleService(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleServiceEmptyID(t *testing.T) {
	r := New()
	req := httptest.NewRequest("GET", "/services/", nil)
	w := httptest.NewRecorder()
	r.handleService(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestDeregisterNotFound(t *testing.T) {
	r := New()
	err := r.Deregister(context.Background(), "nonexistent")
	if err != registry.ErrServiceNotFound {
		t.Errorf("expected ErrServiceNotFound, got %v", err)
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
