# Enhanced gRPC Proxy

The gateway provides gRPC-aware proxying with deadline propagation, metadata transforms, message size limits, authority override, and gRPC health checking. When a route has `grpc.enabled: true`, the gateway sets HTTP/2 protocol headers and applies gRPC-specific processing.

## Configuration

```yaml
routes:
  - id: grpc-api
    path: /pkg.Service/*
    backends:
      - url: http://grpc-backend:50051
    grpc:
      enabled: true
      deadline_propagation: true
      max_recv_msg_size: 4194304    # 4 MB
      max_send_msg_size: 4194304    # 4 MB
      authority: grpc-backend.svc
      metadata_transforms:
        request_map:
          X-Request-Id: x-request-id-meta
          X-Tenant-Id: x-tenant-id
        response_map:
          x-grpc-trace-id: X-Trace-Id
        strip_prefix: x-custom-
        passthrough:
          - authorization
      health_check:
        enabled: true
        service: "pkg.Service"
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable gRPC-aware proxying |
| `deadline_propagation` | bool | `false` | Parse `grpc-timeout` header and set context deadline |
| `max_recv_msg_size` | int | `0` (unlimited) | Max request message size in bytes |
| `max_send_msg_size` | int | `0` (unlimited) | Max response message size in bytes |
| `authority` | string | `""` | Override the `:authority` pseudo-header |
| `metadata_transforms.request_map` | map | `{}` | HTTP header → gRPC metadata name mapping |
| `metadata_transforms.response_map` | map | `{}` | gRPC metadata → HTTP header name mapping |
| `metadata_transforms.strip_prefix` | string | `""` | Auto-strip this prefix from headers and use remainder as metadata name |
| `metadata_transforms.passthrough` | list | `[]` | Headers to pass through as-is |
| `health_check.enabled` | bool | `false` | Use gRPC health protocol (grpc.health.v1) instead of HTTP |
| `health_check.service` | string | `""` | gRPC service name to check (empty = overall server health) |

## Deadline Propagation

When `deadline_propagation: true`, the gateway:

1. Reads the `grpc-timeout` header from the incoming request (format: `5S`, `500m`, `100u`, etc.)
2. Creates a context deadline from the timeout value
3. If the context already has a shorter deadline, uses that instead
4. Updates the `grpc-timeout` header to reflect the remaining time before forwarding

This ensures upstream deadlines are properly propagated to backend gRPC services.

### Timeout Format

| Suffix | Unit |
|--------|------|
| `H` | Hours |
| `M` | Minutes |
| `S` | Seconds |
| `m` | Milliseconds |
| `u` | Microseconds |
| `n` | Nanoseconds |

## Metadata Transforms

Metadata transforms allow bidirectional mapping between HTTP headers and gRPC metadata:

- **`request_map`**: Renames HTTP request headers to gRPC metadata names before forwarding
- **`response_map`**: Renames gRPC response metadata to HTTP headers before returning to the client
- **`strip_prefix`**: Automatically strips a prefix from all matching request headers (e.g., `X-Custom-Foo` → `Foo` with prefix `x-custom-`)
- **`passthrough`**: Explicitly listed headers are preserved as-is

## Message Size Limits

When `max_recv_msg_size` or `max_send_msg_size` are set to a positive value:

- **Receive limit**: The request body is wrapped with a size-checking reader. If the client sends more bytes than allowed, the proxy returns gRPC status `RESOURCE_EXHAUSTED` (code 8).
- **Send limit**: The response writer is wrapped with a size-checking writer. If the backend response exceeds the limit, gRPC status `RESOURCE_EXHAUSTED` is set in response headers.

## gRPC Health Checking

When `health_check.enabled: true`, the backend health checker uses the gRPC health protocol (`grpc.health.v1.Health/Check`) instead of HTTP health checks. The `service` field specifies which service to check; leave empty for overall server health.

## Middleware Position

gRPC preparation happens at step 16 in the middleware chain (`requestTransformMW`), after authentication and before the proxy call. This ensures:

- Auth tokens are validated before gRPC processing
- Metadata transforms can reference authenticated identity
- Deadline context is set before the proxy round-trip

## Admin API

```
GET /grpc-proxy
```

Returns per-route gRPC proxy statistics:

```json
{
  "grpc-api": {
    "enabled": true,
    "deadline_propagation": true,
    "requests": 15000,
    "deadlines_set": 12000,
    "max_recv_msg_size": 4194304,
    "max_send_msg_size": 4194304,
    "authority": "grpc-backend.svc",
    "health_check": true
  }
}
```

## Validation Rules

- `max_recv_msg_size` must be >= 0
- `max_send_msg_size` must be >= 0
- `grpc.enabled` is mutually exclusive with `protocol` translation
