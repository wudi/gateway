---
title: "Load Balancing"
sidebar_position: 1
---

The gateway distributes traffic across backends using configurable algorithms. Each route can specify its own load balancing strategy. All algorithms respect backend health — unhealthy backends are skipped.

## Algorithms

### Round Robin (Default)

Distributes requests evenly across backends. Supports weights for unequal distribution.

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    load_balancer: "round_robin"   # default, can be omitted
    backends:
      - url: "http://backend-1:9000"
        weight: 2
      - url: "http://backend-2:9000"
        weight: 1
```

### Least Connections

Routes each request to the backend with the fewest active connections. Best for backends with variable processing times.

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    load_balancer: "least_conn"
    backends:
      - url: "http://backend-1:9000"
      - url: "http://backend-2:9000"
```

### Consistent Hash

Uses a ketama hash ring to map a request attribute to a specific backend. Ensures the same key always reaches the same backend (useful for caching). Supports keys based on header, cookie, path, or client IP.

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    load_balancer: "consistent_hash"
    consistent_hash:
      key: "header"            # header, cookie, path, or ip
      header_name: "X-User-ID" # required for header/cookie modes
      replicas: 150            # virtual nodes per backend (default 150)
    backends:
      - url: "http://backend-1:9000"
      - url: "http://backend-2:9000"
```

### Least Response Time

Routes requests to the backend with the lowest recent response time, tracked using exponential weighted moving average (EWMA). Prefers backends that haven't been tried yet (cold start).

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    load_balancer: "least_response_time"
    backends:
      - url: "http://backend-1:9000"
      - url: "http://backend-2:9000"
```

## Health Checking

The gateway performs active health checks against each backend at its `/health` path. Unhealthy backends are automatically removed from rotation and re-added when they recover.

## Constraints

- `least_conn`, `consistent_hash`, and `least_response_time` are incompatible with [traffic splits](traffic-management.md)
- When using [traffic splits](traffic-management.md), each group uses its own weighted round-robin

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `load_balancer` | string | `round_robin`, `least_conn`, `consistent_hash`, `least_response_time` |
| `consistent_hash.key` | string | `header`, `cookie`, `path`, or `ip` |
| `consistent_hash.header_name` | string | Header/cookie name (required for header/cookie modes) |
| `consistent_hash.replicas` | int | Virtual nodes per backend (default 150) |

## Per-Tenant Backend Routing

When multi-tenancy is enabled, you can route specific tenants to dedicated backend pools using `tenant_backends`:

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://shared:8080
    tenant_backends:
      acme:
        - url: http://acme-1:8080
        - url: http://acme-2:8080
      startup:
        - url: http://startup-pool:8080
```

Tenants with dedicated backends are routed to their pool; others use the default backends. The same load balancing algorithm configured for the route is used for tenant backend pools. Health checks and marks apply across all balancers.

See [Configuration Reference](../reference/configuration-reference.md#routes) for all fields.
