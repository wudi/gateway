---
title: "gRPC Health Check Server"
sidebar_position: 7
---

The gateway can expose a gRPC health check server implementing the standard `grpc.health.v1.Health` service. This is useful for environments (such as Kubernetes with gRPC health probes) that expect a native gRPC health endpoint rather than HTTP.

## Configuration

```yaml
admin:
  enabled: true
  port: 8081
  grpc_health:
    enabled: true
    address: ":9090"
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `admin.grpc_health.enabled` | bool | false | Enable gRPC health server |
| `admin.grpc_health.address` | string | ":9090" | Listen address for gRPC health server |

## Supported RPCs

The server implements the `grpc.health.v1.Health` service:

- **`Check(HealthCheckRequest)`** -- Returns the current serving status. Returns `SERVING` when the gateway is healthy and `NOT_SERVING` when the gateway is unhealthy or draining.
- **`Watch(HealthCheckRequest)`** -- Streams health status changes. The server sends an initial status immediately and then sends updates whenever the serving status changes.

## Kubernetes Integration

Use the gRPC health server with Kubernetes native gRPC probes:

```yaml
livenessProbe:
  grpc:
    port: 9090
  initialDelaySeconds: 5
  periodSeconds: 10
readinessProbe:
  grpc:
    port: 9090
  periodSeconds: 5
```

## Notes

- The gRPC health server runs on a separate port from both the main listeners and the HTTP admin API.
- The health status reflects the same state as the HTTP `/ready` endpoint.

See [Configuration Reference](../reference/configuration-reference.md#admin) for all admin fields.
