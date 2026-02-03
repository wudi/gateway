package etcd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/example/gateway/internal/config"
	"github.com/example/gateway/internal/registry"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	servicePrefix = "/services/"
	leaseTTL      = 30 // seconds
)

// Registry implements service registry using etcd
type Registry struct {
	client   *clientv3.Client
	leaseID  clientv3.LeaseID
	watchers map[string]context.CancelFunc
	cache    map[string][]*registry.Service
	cacheMu  sync.RWMutex
	watchMu  sync.Mutex
}

// New creates a new etcd registry
func New(cfg config.EtcdConfig) (*Registry, error) {
	etcdCfg := clientv3.Config{
		Endpoints:   cfg.Endpoints,
		DialTimeout: 5 * time.Second,
	}

	if cfg.Username != "" {
		etcdCfg.Username = cfg.Username
		etcdCfg.Password = cfg.Password
	}

	client, err := clientv3.New(etcdCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = client.Status(ctx, cfg.Endpoints[0])
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	return &Registry{
		client:   client,
		watchers: make(map[string]context.CancelFunc),
		cache:    make(map[string][]*registry.Service),
	}, nil
}

// Register registers a service instance with etcd
func (r *Registry) Register(ctx context.Context, service *registry.Service) error {
	// Create a lease
	lease, err := r.client.Grant(ctx, leaseTTL)
	if err != nil {
		return fmt.Errorf("failed to create lease: %w", err)
	}

	// Store the lease ID for keepalive
	r.leaseID = lease.ID

	// Serialize service
	data, err := json.Marshal(service)
	if err != nil {
		return fmt.Errorf("failed to marshal service: %w", err)
	}

	// Store service with lease
	key := serviceKey(service.Name, service.ID)
	_, err = r.client.Put(ctx, key, string(data), clientv3.WithLease(lease.ID))
	if err != nil {
		return fmt.Errorf("failed to register service: %w", err)
	}

	// Start keepalive
	go r.keepAlive(ctx, lease.ID)

	return nil
}

// keepAlive maintains the lease
func (r *Registry) keepAlive(ctx context.Context, leaseID clientv3.LeaseID) {
	keepAliveCh, err := r.client.KeepAlive(ctx, leaseID)
	if err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-keepAliveCh:
			if !ok || resp == nil {
				return
			}
		}
	}
}

// Deregister removes a service instance from etcd
func (r *Registry) Deregister(ctx context.Context, serviceID string) error {
	// Find and delete all keys with this service ID
	prefix := servicePrefix
	resp, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	for _, kv := range resp.Kvs {
		var svc registry.Service
		if err := json.Unmarshal(kv.Value, &svc); err != nil {
			continue
		}
		if svc.ID == serviceID {
			_, err := r.client.Delete(ctx, string(kv.Key))
			if err != nil {
				return fmt.Errorf("failed to deregister service: %w", err)
			}
			return nil
		}
	}

	return registry.ErrServiceNotFound
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

	return r.fetchServices(ctx, serviceName)
}

// DiscoverWithTags returns instances matching specific tags
func (r *Registry) DiscoverWithTags(ctx context.Context, serviceName string, tags []string) ([]*registry.Service, error) {
	services, err := r.fetchServices(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	// Filter by tags
	var filtered []*registry.Service
	for _, svc := range services {
		if hasAllTags(svc.Tags, tags) {
			filtered = append(filtered, svc)
		}
	}

	return filtered, nil
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

// fetchServices fetches services from etcd
func (r *Registry) fetchServices(ctx context.Context, serviceName string) ([]*registry.Service, error) {
	prefix := servicePrefix + serviceName + "/"
	resp, err := r.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("failed to discover services: %w", err)
	}

	services := make([]*registry.Service, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		var svc registry.Service
		if err := json.Unmarshal(kv.Value, &svc); err != nil {
			continue
		}
		if svc.Health == registry.HealthPassing || svc.Health == "" {
			services = append(services, &svc)
		}
	}

	// Update cache
	r.cacheMu.Lock()
	r.cache[serviceName] = services
	r.cacheMu.Unlock()

	return services, nil
}

// Watch subscribes to service changes
func (r *Registry) Watch(ctx context.Context, serviceName string) (<-chan []*registry.Service, error) {
	ch := make(chan []*registry.Service, 10)

	watchCtx, cancel := context.WithCancel(ctx)

	r.watchMu.Lock()
	if existingCancel, ok := r.watchers[serviceName]; ok {
		existingCancel()
	}
	r.watchers[serviceName] = cancel
	r.watchMu.Unlock()

	go r.watchService(watchCtx, serviceName, ch)

	return ch, nil
}

// watchService watches for changes to a service
func (r *Registry) watchService(ctx context.Context, serviceName string, ch chan []*registry.Service) {
	defer close(ch)

	prefix := servicePrefix + serviceName + "/"

	// Send initial state
	services, err := r.fetchServices(ctx, serviceName)
	if err == nil {
		select {
		case ch <- services:
		case <-ctx.Done():
			return
		}
	}

	// Watch for changes
	watchCh := r.client.Watch(ctx, prefix, clientv3.WithPrefix())

	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-watchCh:
			if !ok {
				return
			}
			if resp.Err() != nil {
				continue
			}

			// Fetch updated services
			services, err := r.fetchServices(ctx, serviceName)
			if err != nil {
				continue
			}

			select {
			case ch <- services:
			case <-ctx.Done():
				return
			default:
				// Channel full, cache is updated anyway
			}
		}
	}
}

// Close closes the registry
func (r *Registry) Close() error {
	r.watchMu.Lock()
	for _, cancel := range r.watchers {
		cancel()
	}
	r.watchers = make(map[string]context.CancelFunc)
	r.watchMu.Unlock()

	return r.client.Close()
}

// serviceKey generates the etcd key for a service instance
func serviceKey(serviceName, serviceID string) string {
	return servicePrefix + serviceName + "/" + serviceID
}

// parseServiceKey extracts service name and ID from key
func parseServiceKey(key string) (serviceName, serviceID string) {
	trimmed := strings.TrimPrefix(key, servicePrefix)
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}
