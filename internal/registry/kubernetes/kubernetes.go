package kubernetes

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/wudi/runway/config"
	"github.com/wudi/runway/internal/registry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Registry implements service registry using Kubernetes
type Registry struct {
	client        kubernetes.Interface
	namespace     string
	labelSelector string
	watchers      map[string]context.CancelFunc
	cache         map[string][]*registry.Service
	cacheMu       sync.RWMutex
	watchMu       sync.Mutex
}

// New creates a new Kubernetes registry
func New(cfg config.KubernetesConfig) (*Registry, error) {
	var k8sConfig *rest.Config
	var err error

	if cfg.InCluster {
		k8sConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
		}
	} else {
		k8sConfig, err = clientcmd.BuildConfigFromFlags("", cfg.KubeConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build config from kubeconfig: %w", err)
		}
	}

	client, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	namespace := cfg.Namespace
	if namespace == "" {
		namespace = "default"
	}

	return &Registry{
		client:        client,
		namespace:     namespace,
		labelSelector: cfg.LabelSelector,
		watchers:      make(map[string]context.CancelFunc),
		cache:         make(map[string][]*registry.Service),
	}, nil
}

// Register is a no-op for Kubernetes as services are managed externally
func (r *Registry) Register(ctx context.Context, service *registry.Service) error {
	// In Kubernetes, services are registered via Deployment/Service manifests
	// This is intentionally a no-op
	return nil
}

// Deregister is a no-op for Kubernetes
func (r *Registry) Deregister(ctx context.Context, serviceID string) error {
	// In Kubernetes, services are deregistered via kubectl delete
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

	return r.fetchServices(ctx, serviceName)
}

// DiscoverWithTags returns instances matching specific tags (labels in K8s)
func (r *Registry) DiscoverWithTags(ctx context.Context, serviceName string, tags []string) ([]*registry.Service, error) {
	services, err := r.fetchServices(ctx, serviceName)
	if err != nil {
		return nil, err
	}

	// In Kubernetes, tags are labels - filter by metadata
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

// fetchServices fetches services from Kubernetes Endpoints API
func (r *Registry) fetchServices(ctx context.Context, serviceName string) ([]*registry.Service, error) {
	// Get the Endpoints resource for the service
	endpoints, err := r.client.CoreV1().Endpoints(r.namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get endpoints: %w", err)
	}

	services := make([]*registry.Service, 0)

	for _, subset := range endpoints.Subsets {
		// Get the port
		port := 80
		if len(subset.Ports) > 0 {
			port = int(subset.Ports[0].Port)
		}

		// Add healthy addresses
		for _, addr := range subset.Addresses {
			svc := &registry.Service{
				ID:      fmt.Sprintf("%s-%s", serviceName, addr.IP),
				Name:    serviceName,
				Address: addr.IP,
				Port:    port,
				Health:  registry.HealthPassing,
			}

			// Add pod labels as tags if available
			if addr.TargetRef != nil && addr.TargetRef.Kind == "Pod" {
				pod, err := r.client.CoreV1().Pods(r.namespace).Get(ctx, addr.TargetRef.Name, metav1.GetOptions{})
				if err == nil {
					svc.Tags = labelsToTags(pod.Labels)
					svc.Metadata = pod.Labels
				}
			}

			services = append(services, svc)
		}

		// Add not-ready addresses as unhealthy
		for _, addr := range subset.NotReadyAddresses {
			svc := &registry.Service{
				ID:      fmt.Sprintf("%s-%s", serviceName, addr.IP),
				Name:    serviceName,
				Address: addr.IP,
				Port:    port,
				Health:  registry.HealthCritical,
			}
			services = append(services, svc)
		}
	}

	// Update cache
	r.cacheMu.Lock()
	r.cache[serviceName] = services
	r.cacheMu.Unlock()

	return services, nil
}

// labelsToTags converts Kubernetes labels to tags
func labelsToTags(labels map[string]string) []string {
	tags := make([]string, 0, len(labels))
	for k, v := range labels {
		tags = append(tags, fmt.Sprintf("%s=%s", k, v))
	}
	return tags
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

// watchService watches for changes to service endpoints
func (r *Registry) watchService(ctx context.Context, serviceName string, ch chan []*registry.Service) {
	defer close(ch)

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
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		watcher, err := r.client.CoreV1().Endpoints(r.namespace).Watch(ctx, metav1.ListOptions{
			FieldSelector: fmt.Sprintf("metadata.name=%s", serviceName),
		})
		if err != nil {
			time.Sleep(5 * time.Second)
			continue
		}

	watchLoop:
		for {
			select {
			case <-ctx.Done():
				watcher.Stop()
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					break watchLoop
				}

				switch event.Type {
				case watch.Added, watch.Modified, watch.Deleted:
					services, err := r.fetchServices(ctx, serviceName)
					if err != nil {
						continue
					}

					select {
					case ch <- services:
					case <-ctx.Done():
						watcher.Stop()
						return
					default:
					}
				}
			}
		}

		watcher.Stop()
	}
}

// Close closes the registry
func (r *Registry) Close() error {
	r.watchMu.Lock()
	defer r.watchMu.Unlock()

	for _, cancel := range r.watchers {
		cancel()
	}
	r.watchers = make(map[string]context.CancelFunc)

	return nil
}

// DiscoverBySelector discovers services by label selector
func (r *Registry) DiscoverBySelector(ctx context.Context, labelSelector string) ([]*registry.Service, error) {
	pods, err := r.client.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	services := make([]*registry.Service, 0)
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		// Get first container port
		port := 80
		if len(pod.Spec.Containers) > 0 && len(pod.Spec.Containers[0].Ports) > 0 {
			port = int(pod.Spec.Containers[0].Ports[0].ContainerPort)
		}

		health := registry.HealthPassing
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status != corev1.ConditionTrue {
				health = registry.HealthCritical
				break
			}
		}

		svc := &registry.Service{
			ID:       string(pod.UID),
			Name:     pod.Labels["app"],
			Address:  pod.Status.PodIP,
			Port:     port,
			Tags:     labelsToTags(pod.Labels),
			Metadata: pod.Labels,
			Health:   health,
		}
		services = append(services, svc)
	}

	return services, nil
}

// GetServicePort returns the port for a Kubernetes service
func (r *Registry) GetServicePort(ctx context.Context, serviceName string, portName string) (int, error) {
	svc, err := r.client.CoreV1().Services(r.namespace).Get(ctx, serviceName, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get service: %w", err)
	}

	for _, port := range svc.Spec.Ports {
		if portName == "" || port.Name == portName {
			return int(port.Port), nil
		}
	}

	// Check for port annotation
	if portStr, ok := svc.Annotations["runway.port"]; ok {
		if port, err := strconv.Atoi(portStr); err == nil {
			return port, nil
		}
	}

	return 0, fmt.Errorf("port not found for service %s", serviceName)
}
