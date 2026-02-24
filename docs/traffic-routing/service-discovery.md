---
title: "Service Discovery"
sidebar_position: 9
---

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

### DNS SRV

Discover backends via DNS SRV records (RFC 2782). Works with any DNS-based service discovery system — Consul DNS interface, Kubernetes CoreDNS, AWS Cloud Map, HashiCorp Nomad, or any compliant DNS server. No external client library required.

```yaml
registry:
  type: "dns"
  dns:
    domain: "service.consul"       # required: base domain for SRV queries
    protocol: "tcp"                # default "tcp"
    nameserver: "10.0.0.53:8600"   # optional: custom DNS server
    poll_interval: 30s             # default 30s
```

The provider queries `_<service>._<protocol>.<domain>` SRV records and resolves each target hostname to an IP address. SRV priority and weight are stored in service metadata (`srv_priority`, `srv_weight`, `srv_target`). Results are sorted by priority ascending (lower = preferred per RFC 2782), then weight descending.

DNS SRV is a read-only registry — `Register` and `Deregister` are no-ops. Health is always reported as `passing` since the gateway's own health checker probes backends separately. Tags are not supported (DNS SRV has no tag concept); `DiscoverWithTags` returns all instances.

Common domain patterns:

| System | Domain | Example SRV query |
|--------|--------|-------------------|
| Consul DNS | `service.consul` | `_web._tcp.service.consul` |
| Kubernetes CoreDNS | `svc.cluster.local` | `_http._tcp.web.default.svc.cluster.local` |
| AWS Cloud Map | `<namespace>` | `_api._tcp.production.local` |

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
| `registry.type` | string | `consul`, `etcd`, `kubernetes`, `memory`, or `dns` |
| `service.name` | string | Service name to look up in the registry |
| `service.tags` | []string | Filter service instances by tags |

See [Configuration Reference](../reference/configuration-reference.md#registry) for all fields.
