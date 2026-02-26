package consul

import (
	"context"
	"fmt"
	"sync"
	"time"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/registry"
)

// Registry implements service registry using Consul
type Registry struct {
	client     *consulapi.Client
	datacenter string
	watchers   map[string]context.CancelFunc
	cache      map[string][]*registry.Service
	cacheMu    sync.RWMutex
	watcherMu  sync.Mutex
}

// New creates a new Consul registry
func New(cfg config.ConsulConfig) (*Registry, error) {
	consulCfg := consulapi.DefaultConfig()
	consulCfg.Address = cfg.Address
	consulCfg.Scheme = cfg.Scheme
	consulCfg.Datacenter = cfg.Datacenter

	if cfg.Token != "" {
		consulCfg.Token = cfg.Token
	}

	client, err := consulapi.NewClient(consulCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create Consul client: %w", err)
	}

	// Test connection
	_, err = client.Agent().Self()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Consul: %w", err)
	}

	return &Registry{
		client:     client,
		datacenter: cfg.Datacenter,
		watchers:   make(map[string]context.CancelFunc),
		cache:      make(map[string][]*registry.Service),
	}, nil
}

// Register registers a service instance with Consul
func (r *Registry) Register(ctx context.Context, service *registry.Service) error {
	registration := &consulapi.AgentServiceRegistration{
		ID:      service.ID,
		Name:    service.Name,
		Address: service.Address,
		Port:    service.Port,
		Tags:    service.Tags,
		Meta:    service.Metadata,
		Check: &consulapi.AgentServiceCheck{
			HTTP:                           fmt.Sprintf("http://%s:%d/health", service.Address, service.Port),
			Interval:                       "10s",
			Timeout:                        "5s",
			DeregisterCriticalServiceAfter: "30s",
		},
	}

	if err := r.client.Agent().ServiceRegister(registration); err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	return nil
}

// Deregister removes a service instance from Consul
func (r *Registry) Deregister(ctx context.Context, serviceID string) error {
	if err := r.client.Agent().ServiceDeregister(serviceID); err != nil {
		return fmt.Errorf("failed to deregister service: %w", err)
	}
	return nil
}

// Discover returns all healthy instances of a service
func (r *Registry) Discover(ctx context.Context, serviceName string) ([]*registry.Service, error) {
	// Check cache first
	r.cacheMu.RLock()
	if cached, ok := r.cache[serviceName]; ok {
		r.cacheMu.RUnlock()
		return cached, nil
	}
	r.cacheMu.RUnlock()

	return r.fetchServices(serviceName, nil)
}

// DiscoverWithTags returns instances matching specific tags
func (r *Registry) DiscoverWithTags(ctx context.Context, serviceName string, tags []string) ([]*registry.Service, error) {
	return r.fetchServices(serviceName, tags)
}

// fetchServices fetches services from Consul
func (r *Registry) fetchServices(serviceName string, tags []string) ([]*registry.Service, error) {
	queryOpts := &consulapi.QueryOptions{
		Datacenter: r.datacenter,
	}

	// Use Health API to get only healthy services
	var entries []*consulapi.ServiceEntry
	var err error

	if len(tags) > 0 {
		// Filter by first tag, then filter rest in memory
		entries, _, err = r.client.Health().ServiceMultipleTags(serviceName, tags, true, queryOpts)
	} else {
		entries, _, err = r.client.Health().Service(serviceName, "", true, queryOpts)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to discover services: %w", err)
	}

	services := make([]*registry.Service, 0, len(entries))
	for _, entry := range entries {
		svc := &registry.Service{
			ID:       entry.Service.ID,
			Name:     entry.Service.Service,
			Address:  entry.Service.Address,
			Port:     entry.Service.Port,
			Tags:     entry.Service.Tags,
			Metadata: entry.Service.Meta,
			Health:   convertHealth(entry.Checks),
		}

		// Use node address if service address is empty
		if svc.Address == "" {
			svc.Address = entry.Node.Address
		}

		services = append(services, svc)
	}

	// Update cache
	r.cacheMu.Lock()
	r.cache[serviceName] = services
	r.cacheMu.Unlock()

	return services, nil
}

// convertHealth converts Consul health checks to registry health status
func convertHealth(checks consulapi.HealthChecks) registry.HealthStatus {
	for _, check := range checks {
		if check.Status == consulapi.HealthCritical {
			return registry.HealthCritical
		}
		if check.Status == consulapi.HealthWarning {
			return registry.HealthWarning
		}
	}
	return registry.HealthPassing
}

// Watch subscribes to service changes using Consul blocking queries
func (r *Registry) Watch(ctx context.Context, serviceName string) (<-chan []*registry.Service, error) {
	ch := make(chan []*registry.Service, 10)

	watchCtx, cancel := context.WithCancel(ctx)

	r.watcherMu.Lock()
	// Cancel any existing watcher for this service
	if existingCancel, ok := r.watchers[serviceName]; ok {
		existingCancel()
	}
	r.watchers[serviceName] = cancel
	r.watcherMu.Unlock()

	go r.watchService(watchCtx, serviceName, ch)

	return ch, nil
}

// watchService performs blocking queries to watch for changes
func (r *Registry) watchService(ctx context.Context, serviceName string, ch chan []*registry.Service) {
	defer close(ch)

	var lastIndex uint64

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		queryOpts := &consulapi.QueryOptions{
			Datacenter: r.datacenter,
			WaitIndex:  lastIndex,
			WaitTime:   30 * time.Second,
		}

		entries, meta, err := r.client.Health().Service(serviceName, "", true, queryOpts)
		if err != nil {
			// Log error and retry after delay
			time.Sleep(5 * time.Second)
			continue
		}

		// Check if index changed (new data available)
		if meta.LastIndex == lastIndex {
			continue
		}
		lastIndex = meta.LastIndex

		services := make([]*registry.Service, 0, len(entries))
		for _, entry := range entries {
			svc := &registry.Service{
				ID:       entry.Service.ID,
				Name:     entry.Service.Service,
				Address:  entry.Service.Address,
				Port:     entry.Service.Port,
				Tags:     entry.Service.Tags,
				Metadata: entry.Service.Meta,
				Health:   convertHealth(entry.Checks),
			}
			if svc.Address == "" {
				svc.Address = entry.Node.Address
			}
			services = append(services, svc)
		}

		// Update cache
		r.cacheMu.Lock()
		r.cache[serviceName] = services
		r.cacheMu.Unlock()

		// Send to channel
		select {
		case ch <- services:
		case <-ctx.Done():
			return
		default:
			// Channel full, update cache anyway
		}
	}
}

// Close closes the registry and cancels all watchers
func (r *Registry) Close() error {
	r.watcherMu.Lock()
	defer r.watcherMu.Unlock()

	for _, cancel := range r.watchers {
		cancel()
	}
	r.watchers = make(map[string]context.CancelFunc)

	return nil
}

// RegisterWithCheck registers a service with a custom health check
func (r *Registry) RegisterWithCheck(ctx context.Context, service *registry.Service, checkURL string, interval string) error {
	registration := &consulapi.AgentServiceRegistration{
		ID:      service.ID,
		Name:    service.Name,
		Address: service.Address,
		Port:    service.Port,
		Tags:    service.Tags,
		Meta:    service.Metadata,
	}

	if checkURL != "" {
		registration.Check = &consulapi.AgentServiceCheck{
			HTTP:                           checkURL,
			Interval:                       interval,
			Timeout:                        "5s",
			DeregisterCriticalServiceAfter: "1m",
		}
	}

	if err := r.client.Agent().ServiceRegister(registration); err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	return nil
}
