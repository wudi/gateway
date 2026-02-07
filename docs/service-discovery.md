# Service Discovery

The gateway can discover backend addresses dynamically from a service registry instead of hardcoding URLs. When a route uses service discovery, the gateway watches for changes and updates its backend list automatically.

Configure the registry type globally, then reference services by name in route definitions.

## Registry Types

### Memory

An in-memory registry with a REST API for manual registration. Useful for development and testing.

```yaml
registry:
  type: "memory"
  memory:
    api_enabled: true
    api_port: 8082
```

Manage service instances via the REST API:

```bash
# Register a service instance
curl -X POST http://localhost:8082/services \
  -H "Content-Type: application/json" \
  -d '{"name":"users-service","address":"http://localhost:9001","tags":["production"]}'

# List registered services
curl http://localhost:8082/services

# Deregister a service instance
curl -X DELETE http://localhost:8082/services \
  -H "Content-Type: application/json" \
  -d '{"name":"users-service","address":"http://localhost:9001"}'
```

### Consul

HashiCorp Consul integration with service health checking, datacenter awareness, and namespace support.

```yaml
registry:
  type: "consul"
  consul:
    address: "localhost:8500"
    scheme: "http"
    datacenter: "dc1"
    token: "${CONSUL_TOKEN}"
    namespace: "production"
```

### Etcd

Etcd key-value store integration with optional TLS and authentication.

```yaml
registry:
  type: "etcd"
  etcd:
    endpoints:
      - "etcd-1:2379"
      - "etcd-2:2379"
    username: "${ETCD_USER}"
    password: "${ETCD_PASS}"
    tls:
      enabled: true
      cert_file: "/etc/certs/etcd-client.crt"
      key_file: "/etc/certs/etcd-client.key"
      ca_file: "/etc/certs/etcd-ca.crt"
```

### Kubernetes

Discover services from Kubernetes endpoints with label-based filtering. Supports both in-cluster and out-of-cluster authentication.

```yaml
registry:
  type: "kubernetes"
  kubernetes:
    namespace: "default"
    label_selector: "app=myservice"
    in_cluster: true
    # Or use kubeconfig for out-of-cluster:
    # kube_config: "/home/user/.kube/config"
```

## Using Service Discovery in Routes

Reference a service name instead of (or alongside) static backends:

```yaml
routes:
  - id: "users-api"
    path: "/api/users"
    path_prefix: true
    service:
      name: "users-service"
      tags: ["production"]
```

The gateway watches the registry for changes and updates the backend list without requiring a config reload. If a service instance becomes unhealthy, it is removed from the load balancer rotation.

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `registry.type` | string | `consul`, `etcd`, `kubernetes`, or `memory` |
| `service.name` | string | Service name to look up in the registry |
| `service.tags` | []string | Filter service instances by tags |

See [Configuration Reference](configuration-reference.md#registry) for all fields.
