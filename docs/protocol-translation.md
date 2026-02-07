# Protocol Translation

The gateway can translate between protocols, enabling HTTP clients to communicate with gRPC backends. It also provides native WebSocket proxying.

## HTTP-to-gRPC Translation

Translates incoming HTTP/JSON requests into gRPC calls using server reflection or REST-to-gRPC mappings. The gRPC backend's protobuf descriptors are fetched dynamically via reflection and cached.

### Basic Configuration

The simplest setup uses gRPC server reflection to discover services:

```yaml
routes:
  - id: "grpc-api"
    path: "/grpc"
    path_prefix: true
    backends:
      - url: "http://grpc-backend:50051"
    protocol:
      type: "http_to_grpc"
      grpc:
        timeout: 30s
        descriptor_cache_ttl: 5m
```

With this configuration, requests to `/grpc/pkg.Service/Method` are translated to gRPC calls to `pkg.Service.Method`.

### Service-Scoped Mode

Fix the service name so clients only need to specify the method:

```yaml
protocol:
  type: "http_to_grpc"
  grpc:
    service: "mypackage.MyService"
    timeout: 30s
```

Requests to `/grpc/GetUser` translate to `mypackage.MyService.GetUser`.

### REST-to-gRPC Mappings

Map RESTful HTTP endpoints to gRPC methods with path parameter extraction:

```yaml
protocol:
  type: "http_to_grpc"
  grpc:
    service: "mypackage.UserService"
    mappings:
      - http_method: "GET"
        http_path: "/users/:user_id"
        grpc_method: "GetUser"
        body: ""                    # params only (no body)
      - http_method: "POST"
        http_path: "/users"
        grpc_method: "CreateUser"
        body: "*"                   # whole body maps to request
      - http_method: "PUT"
        http_path: "/users/{user_id}"
        grpc_method: "UpdateUser"
        body: "user"               # body nested under "user" field
```

Path parameters support both `:param` and `{param}` syntax.

**Body mapping modes:**
- `""` (empty) — only path/query params populate the request
- `"*"` — entire JSON body is merged into the gRPC request
- `"field"` — JSON body is nested under the named field

### TLS to gRPC Backend

When TLS is enabled, `ca_file` is required for server certificate verification. `cert_file` and `key_file` are optional (for mutual TLS to the gRPC backend).

```yaml
protocol:
  type: "http_to_grpc"
  grpc:
    service: "mypackage.MyService"
    tls:
      enabled: true
      cert_file: "/etc/certs/grpc-client.crt"   # optional mTLS
      key_file: "/etc/certs/grpc-client.key"     # optional mTLS
      ca_file: "/etc/certs/grpc-ca.crt"          # required
```

## gRPC Passthrough

For native gRPC-to-gRPC proxying (no translation), use the `grpc` passthrough mode:

```yaml
routes:
  - id: "grpc-proxy"
    path: "/mypackage.MyService"
    path_prefix: true
    backends:
      - url: "http://grpc-backend:50051"
    grpc:
      enabled: true
```

Note: `grpc.enabled` and `protocol.type` are mutually exclusive.

## WebSocket Proxying

The gateway transparently proxies WebSocket connections. When a client sends an HTTP Upgrade request, the gateway hijacks the connection and establishes a bidirectional tunnel to the backend.

```yaml
routes:
  - id: "ws"
    path: "/ws"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    websocket:
      enabled: true
      read_buffer_size: 4096
      write_buffer_size: 4096
      read_timeout: 60s
      write_timeout: 10s
      ping_interval: 30s
      pong_timeout: 10s
```

WebSocket connections bypass the cache and circuit breaker (they return early in the middleware chain).

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `protocol.type` | string | `http_to_grpc` |
| `protocol.grpc.service` | string | Fully-qualified gRPC service name |
| `protocol.grpc.timeout` | duration | Per-call timeout (default 30s) |
| `protocol.grpc.descriptor_cache_ttl` | duration | Reflection cache TTL (default 5m) |
| `protocol.grpc.mappings` | []GRPCMethodMapping | REST-to-gRPC path mappings |
| `grpc.enabled` | bool | Enable gRPC passthrough (mutually exclusive with protocol) |
| `websocket.enabled` | bool | Enable WebSocket proxying |
| `websocket.ping_interval` | duration | Keep-alive ping interval |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
