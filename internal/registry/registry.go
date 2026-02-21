package registry

import (
	"context"
	"fmt"
)

// HealthStatus represents the health status of a service
type HealthStatus string

const (
	HealthPassing  HealthStatus = "passing"
	HealthWarning  HealthStatus = "warning"
	HealthCritical HealthStatus = "critical"
	HealthUnknown  HealthStatus = "unknown"
)

// Service represents a service instance
type Service struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Address  string            `json:"address"`
	Port     int               `json:"port"`
	Tags     []string          `json:"tags,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Health   HealthStatus      `json:"health"`
}

// URL returns the full URL for the service
func (s *Service) URL() string {
	return fmt.Sprintf("http://%s:%d", s.Address, s.Port)
}

// Registry defines the interface for service discovery
type Registry interface {
	// Register registers a service instance
	Register(ctx context.Context, service *Service) error

	// Deregister removes a service instance
	Deregister(ctx context.Context, serviceID string) error

	// Discover returns all healthy instances of a service
	Discover(ctx context.Context, serviceName string) ([]*Service, error)

	// DiscoverWithTags returns instances matching specific tags
	DiscoverWithTags(ctx context.Context, serviceName string, tags []string) ([]*Service, error)

	// Watch subscribes to service changes
	Watch(ctx context.Context, serviceName string) (<-chan []*Service, error)

	// Close closes the registry connection
	Close() error
}

// RegistryType represents the type of registry
type RegistryType string

const (
	TypeConsul     RegistryType = "consul"
	TypeEtcd       RegistryType = "etcd"
	TypeKubernetes RegistryType = "kubernetes"
	TypeMemory     RegistryType = "memory"
	TypeDNS        RegistryType = "dns"
)

// ErrServiceNotFound is returned when a service is not found
var ErrServiceNotFound = fmt.Errorf("service not found")

// ErrRegistryUnavailable is returned when the registry is not available
var ErrRegistryUnavailable = fmt.Errorf("registry unavailable")
