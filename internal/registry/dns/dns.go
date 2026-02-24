package dns

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wudi/gateway/config"
	"github.com/wudi/gateway/internal/registry"
)

// resolver abstracts DNS lookups for testability.
type resolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// netResolver wraps *net.Resolver to implement the resolver interface.
type netResolver struct {
	r *net.Resolver
}

func (n *netResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return n.r.LookupSRV(ctx, service, proto, name)
}

func (n *netResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return n.r.LookupHost(ctx, host)
}

// Registry implements service discovery via DNS SRV records (RFC 2782).
type Registry struct {
	domain       string
	protocol     string
	pollInterval time.Duration
	resolver     resolver
	cache        map[string][]*registry.Service
	cacheMu      sync.RWMutex
	watchers     map[string]context.CancelFunc
	watchMu      sync.Mutex
}

// New creates a new DNS SRV registry.
func New(cfg config.DNSSRVConfig) (*Registry, error) {
	if cfg.Domain == "" {
		return nil, fmt.Errorf("dns registry: domain is required")
	}

	protocol := cfg.Protocol
	if protocol == "" {
		protocol = "tcp"
	}

	pollInterval := cfg.PollInterval
	if pollInterval == 0 {
		pollInterval = 30 * time.Second
	}

	var r *net.Resolver
	if cfg.Nameserver != "" {
		r = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", cfg.Nameserver)
			},
		}
	} else {
		r = net.DefaultResolver
	}

	return &Registry{
		domain:       cfg.Domain,
		protocol:     protocol,
		pollInterval: pollInterval,
		resolver:     &netResolver{r: r},
		cache:        make(map[string][]*registry.Service),
		watchers:     make(map[string]context.CancelFunc),
	}, nil
}

// Register is a no-op for DNS SRV (read-only registry).
func (r *Registry) Register(_ context.Context, _ *registry.Service) error {
	return nil
}

// Deregister is a no-op for DNS SRV (read-only registry).
func (r *Registry) Deregister(_ context.Context, _ string) error {
	return nil
}

// Discover returns all instances of a service resolved from SRV records.
func (r *Registry) Discover(ctx context.Context, serviceName string) ([]*registry.Service, error) {
	services, err := r.fetchServices(ctx, serviceName)
	if err != nil {
		// Return cached state on failure.
		r.cacheMu.RLock()
		cached, ok := r.cache[serviceName]
		r.cacheMu.RUnlock()
		if ok {
			return cached, nil
		}
		return nil, err
	}
	return services, nil
}

// DiscoverWithTags delegates to Discover (DNS SRV has no tag concept).
func (r *Registry) DiscoverWithTags(ctx context.Context, serviceName string, _ []string) ([]*registry.Service, error) {
	return r.Discover(ctx, serviceName)
}

// Watch subscribes to service changes via polling.
func (r *Registry) Watch(ctx context.Context, serviceName string) (<-chan []*registry.Service, error) {
	ch := make(chan []*registry.Service, 10)

	watchCtx, cancel := context.WithCancel(ctx)

	r.watchMu.Lock()
	if existingCancel, ok := r.watchers[serviceName]; ok {
		existingCancel()
	}
	r.watchers[serviceName] = cancel
	r.watchMu.Unlock()

	go r.pollService(watchCtx, serviceName, ch)

	return ch, nil
}

// Close cancels all watcher goroutines.
func (r *Registry) Close() error {
	r.watchMu.Lock()
	defer r.watchMu.Unlock()

	for _, cancel := range r.watchers {
		cancel()
	}
	r.watchers = make(map[string]context.CancelFunc)

	return nil
}

// fetchServices performs SRV + A lookups and updates the cache.
func (r *Registry) fetchServices(ctx context.Context, serviceName string) ([]*registry.Service, error) {
	_, srvs, err := r.resolver.LookupSRV(ctx, serviceName, r.protocol, r.domain)
	if err != nil {
		return nil, fmt.Errorf("dns srv lookup failed for %s: %w", serviceName, err)
	}

	services := make([]*registry.Service, 0, len(srvs))
	for _, srv := range srvs {
		target := strings.TrimSuffix(srv.Target, ".")
		addr := r.resolveTarget(ctx, target)

		services = append(services, &registry.Service{
			ID:      fmt.Sprintf("%s-%s-%d", serviceName, target, srv.Port),
			Name:    serviceName,
			Address: addr,
			Port:    int(srv.Port),
			Health:  registry.HealthPassing,
			Metadata: map[string]string{
				"srv_priority": strconv.FormatUint(uint64(srv.Priority), 10),
				"srv_weight":   strconv.FormatUint(uint64(srv.Weight), 10),
				"srv_target":   target,
			},
		})
	}

	// Sort by priority ascending (lower = preferred), then weight descending.
	sort.Slice(services, func(i, j int) bool {
		pi, _ := strconv.Atoi(services[i].Metadata["srv_priority"])
		pj, _ := strconv.Atoi(services[j].Metadata["srv_priority"])
		if pi != pj {
			return pi < pj
		}
		wi, _ := strconv.Atoi(services[i].Metadata["srv_weight"])
		wj, _ := strconv.Atoi(services[j].Metadata["srv_weight"])
		return wi > wj
	})

	r.cacheMu.Lock()
	r.cache[serviceName] = services
	r.cacheMu.Unlock()

	return services, nil
}

// resolveTarget resolves an SRV target hostname to an IP address.
// Prefers IPv4. Falls back to the raw hostname on resolution failure.
func (r *Registry) resolveTarget(ctx context.Context, target string) string {
	// If target is already an IP, return it directly.
	if net.ParseIP(target) != nil {
		return target
	}

	addrs, err := r.resolver.LookupHost(ctx, target)
	if err != nil || len(addrs) == 0 {
		return target
	}

	// Prefer IPv4.
	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && ip.To4() != nil {
			return addr
		}
	}

	return addrs[0]
}

// pollService polls DNS at the configured interval and sends updates on changes.
func (r *Registry) pollService(ctx context.Context, serviceName string, ch chan []*registry.Service) {
	defer close(ch)

	// Send initial state.
	services, err := r.fetchServices(ctx, serviceName)
	if err == nil {
		select {
		case ch <- services:
		case <-ctx.Done():
			return
		}
	}

	// Track the last known state for change detection.
	lastServices := services

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newServices, err := r.fetchServices(ctx, serviceName)
			if err != nil {
				// Continue with cached state on DNS failure.
				continue
			}

			if !servicesEqual(lastServices, newServices) {
				lastServices = newServices
				select {
				case ch <- newServices:
				default:
					// Channel full; drop update (cache is still current).
				}
			}
		}
	}
}

// servicesEqual compares two service slices by their sorted IDs.
func servicesEqual(a, b []*registry.Service) bool {
	if len(a) != len(b) {
		return false
	}

	aIDs := make([]string, len(a))
	bIDs := make([]string, len(b))
	for i := range a {
		aIDs[i] = a[i].ID
	}
	for i := range b {
		bIDs[i] = b[i].ID
	}
	sort.Strings(aIDs)
	sort.Strings(bIDs)

	for i := range aIDs {
		if aIDs[i] != bIDs[i] {
			return false
		}
	}
	return true
}
