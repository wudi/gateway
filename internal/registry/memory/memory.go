package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/example/gateway/internal/registry"
	"github.com/google/uuid"
)

// Registry implements an in-memory service registry with REST API
type Registry struct {
	services  map[string]*registry.Service
	watchers  map[string][]chan []*registry.Service
	mu        sync.RWMutex
	apiServer *http.Server
	apiPort   int
}

// New creates a new in-memory registry
func New() *Registry {
	return &Registry{
		services: make(map[string]*registry.Service),
		watchers: make(map[string][]chan []*registry.Service),
	}
}

// NewWithAPI creates a new in-memory registry with REST API enabled
func NewWithAPI(port int) (*Registry, error) {
	r := New()
	r.apiPort = port

	if err := r.startAPI(port); err != nil {
		return nil, err
	}

	return r, nil
}

// Register registers a service instance
func (r *Registry) Register(ctx context.Context, service *registry.Service) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if service.ID == "" {
		service.ID = uuid.New().String()
	}
	if service.Health == "" {
		service.Health = registry.HealthPassing
	}

	r.services[service.ID] = service
	r.notifyWatchers(service.Name)

	return nil
}

// Deregister removes a service instance
func (r *Registry) Deregister(ctx context.Context, serviceID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	service, exists := r.services[serviceID]
	if !exists {
		return registry.ErrServiceNotFound
	}

	serviceName := service.Name
	delete(r.services, serviceID)
	r.notifyWatchers(serviceName)

	return nil
}

// Discover returns all healthy instances of a service
func (r *Registry) Discover(ctx context.Context, serviceName string) ([]*registry.Service, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*registry.Service
	for _, svc := range r.services {
		if svc.Name == serviceName && svc.Health == registry.HealthPassing {
			result = append(result, svc)
		}
	}

	return result, nil
}

// DiscoverWithTags returns instances matching specific tags
func (r *Registry) DiscoverWithTags(ctx context.Context, serviceName string, tags []string) ([]*registry.Service, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*registry.Service
	for _, svc := range r.services {
		if svc.Name == serviceName && svc.Health == registry.HealthPassing && hasAllTags(svc.Tags, tags) {
			result = append(result, svc)
		}
	}

	return result, nil
}

// hasAllTags checks if service has all required tags
func hasAllTags(serviceTags, requiredTags []string) bool {
	if len(requiredTags) == 0 {
		return true
	}

	tagSet := make(map[string]bool)
	for _, t := range serviceTags {
		tagSet[t] = true
	}

	for _, t := range requiredTags {
		if !tagSet[t] {
			return false
		}
	}
	return true
}

// Watch subscribes to service changes
func (r *Registry) Watch(ctx context.Context, serviceName string) (<-chan []*registry.Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ch := make(chan []*registry.Service, 10)
	r.watchers[serviceName] = append(r.watchers[serviceName], ch)

	// Send current state
	var current []*registry.Service
	for _, svc := range r.services {
		if svc.Name == serviceName && svc.Health == registry.HealthPassing {
			current = append(current, svc)
		}
	}

	go func() {
		select {
		case ch <- current:
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}()

	// Clean up when context is done
	go func() {
		<-ctx.Done()
		r.removeWatcher(serviceName, ch)
	}()

	return ch, nil
}

// removeWatcher removes a watcher channel
func (r *Registry) removeWatcher(serviceName string, ch chan []*registry.Service) {
	r.mu.Lock()
	defer r.mu.Unlock()

	watchers := r.watchers[serviceName]
	for i, w := range watchers {
		if w == ch {
			r.watchers[serviceName] = append(watchers[:i], watchers[i+1:]...)
			close(ch)
			break
		}
	}
}

// notifyWatchers notifies all watchers of a service change (caller must hold lock)
func (r *Registry) notifyWatchers(serviceName string) {
	var services []*registry.Service
	for _, svc := range r.services {
		if svc.Name == serviceName && svc.Health == registry.HealthPassing {
			services = append(services, svc)
		}
	}

	for _, ch := range r.watchers[serviceName] {
		select {
		case ch <- services:
		default:
			// Channel full, skip
		}
	}
}

// Close closes the registry
func (r *Registry) Close() error {
	if r.apiServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return r.apiServer.Shutdown(ctx)
	}
	return nil
}

// GetAll returns all registered services
func (r *Registry) GetAll() []*registry.Service {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*registry.Service, 0, len(r.services))
	for _, svc := range r.services {
		result = append(result, svc)
	}
	return result
}

// REST API handlers

func (r *Registry) startAPI(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/services", r.handleServices)
	mux.HandleFunc("/services/", r.handleService)
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	r.apiServer = &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		if err := r.apiServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("Registry API error: %v\n", err)
		}
	}()

	return nil
}

func (r *Registry) handleServices(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch req.Method {
	case http.MethodGet:
		// List all services or filter by name
		name := req.URL.Query().Get("name")
		services := r.GetAll()

		if name != "" {
			var filtered []*registry.Service
			for _, svc := range services {
				if svc.Name == name {
					filtered = append(filtered, svc)
				}
			}
			services = filtered
		}

		json.NewEncoder(w).Encode(services)

	case http.MethodPost:
		// Register a new service
		var svc registry.Service
		if err := json.NewDecoder(req.Body).Decode(&svc); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}

		if svc.Name == "" {
			http.Error(w, `{"error":"name is required"}`, http.StatusBadRequest)
			return
		}

		if svc.Address == "" {
			http.Error(w, `{"error":"address is required"}`, http.StatusBadRequest)
			return
		}

		if svc.Port == 0 {
			http.Error(w, `{"error":"port is required"}`, http.StatusBadRequest)
			return
		}

		if err := r.Register(req.Context(), &svc); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(svc)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (r *Registry) handleService(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract service ID from path
	id := req.URL.Path[len("/services/"):]
	if id == "" {
		http.Error(w, `{"error":"service id required"}`, http.StatusBadRequest)
		return
	}

	switch req.Method {
	case http.MethodGet:
		r.mu.RLock()
		svc, exists := r.services[id]
		r.mu.RUnlock()

		if !exists {
			http.Error(w, `{"error":"service not found"}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(svc)

	case http.MethodPut:
		// Update service
		var svc registry.Service
		if err := json.NewDecoder(req.Body).Decode(&svc); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}
		svc.ID = id

		if err := r.Register(req.Context(), &svc); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(svc)

	case http.MethodDelete:
		if err := r.Deregister(req.Context(), id); err != nil {
			if err == registry.ErrServiceNotFound {
				http.Error(w, `{"error":"service not found"}`, http.StatusNotFound)
				return
			}
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
