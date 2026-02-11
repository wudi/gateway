# Protocol Translation

The gateway can translate between protocols, enabling HTTP clients to communicate with gRPC and Thrift backends. It also provides native WebSocket proxying.

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

## HTTP-to-Thrift Translation

Translates incoming HTTP/JSON requests into Thrift RPC calls. Unlike gRPC, Thrift has no reflection API, so service schemas must be provided by the user — either via `.thrift` IDL files or inline in the YAML config. Dynamic invocation uses Apache Thrift TProtocol primitives to construct and read binary messages without generated code.

### Basic Configuration

The simplest setup uses a fixed service with path-based method resolution:

```yaml
routes:
  - id: "thrift-api"
    path: "/thrift"
    path_prefix: true
    backends:
      - url: "http://thrift-backend:9090"
    protocol:
      type: "http_to_thrift"
      thrift:
        idl_file: "/etc/idl/user_service.thrift"
        service: "UserService"
        timeout: 30s
```

With this configuration, requests to `/thrift/GetUser` with a JSON body `{"id": "123"}` are translated to a Thrift `UserService.GetUser` call. The last path segment is used as the method name.

### IDL-Free Mode (Inline Schema)

Instead of distributing `.thrift` IDL files, you can define method schemas directly in the YAML config. The gateway builds the same internal schema structures, so the behavior is identical to the IDL-based approach.

```yaml
routes:
  - id: "thrift-api"
    path: "/thrift"
    path_prefix: true
    backends:
      - url: "http://thrift-backend:9090"
    protocol:
      type: "http_to_thrift"
      thrift:
        service: "UserService"
        timeout: 30s
        methods:
          GetUser:
            args:
              - id: 1
                name: "user_id"
                type: "string"
            result:
              - id: 0
                name: "success"
                type: "struct"
                struct: "User"
          CreateUser:
            args:
              - id: 1
                name: "user"
                type: "struct"
                struct: "User"
            void: true
          NotifyUser:
            args:
              - id: 1
                name: "id"
                type: "string"
              - id: 2
                name: "message"
                type: "string"
            oneway: true
        structs:
          User:
            - id: 1
              name: "name"
              type: "string"
            - id: 2
              name: "age"
              type: "i32"
            - id: 3
              name: "tags"
              type: "list"
              elem: "string"
          Address:
            - id: 1
              name: "street"
              type: "string"
            - id: 2
              name: "zip"
              type: "i32"
        enums:
          Status:
            ACTIVE: 1
            INACTIVE: 2
```

**Key points:**

- `idl_file` and `methods` are mutually exclusive — use one or the other.
- Each field requires an `id` (the Thrift field ID) and `type`. Field IDs in `args` must be > 0. In `result`, field ID 0 is the success return value; IDs 1+ are exceptions.
- The `type` field accepts: `bool`, `byte`, `i16`, `i32`, `i64`, `double`, `string`, `binary`, `struct`, `list`, `set`, `map`, or an enum name defined in `enums`.
- For `struct` types, specify the struct name in the `struct` field. For `list`/`set`, specify the element type in `elem`. For `map`, specify `key` and `value` types.
- Methods with `void: true` return no value. Methods with `oneway: true` are fire-and-forget.
- Struct and enum names referenced in fields must be defined in the `structs` and `enums` maps.

### Fixed Method Mode

Fix both service and method for a single-purpose route:

```yaml
protocol:
  type: "http_to_thrift"
  thrift:
    idl_file: "/etc/idl/user_service.thrift"
    service: "UserService"
    method: "GetUser"
    timeout: 30s
```

All requests to this route invoke `UserService.GetUser` regardless of the URL path.

### REST-to-Thrift Mappings

Map RESTful HTTP endpoints to Thrift methods with path parameter extraction:

```yaml
protocol:
  type: "http_to_thrift"
  thrift:
    idl_file: "/etc/idl/user_service.thrift"
    service: "UserService"
    mappings:
      - http_method: "GET"
        http_path: "/users/:user_id"
        thrift_method: "GetUser"
        body: ""
      - http_method: "POST"
        http_path: "/users"
        thrift_method: "CreateUser"
        body: "*"
      - http_method: "PUT"
        http_path: "/users/{user_id}"
        thrift_method: "UpdateUser"
        body: "*"
```

Path parameters support both `:param` and `{param}` syntax. Body mapping modes are the same as gRPC: `""` (params only), `"*"` (whole body), or `"field"` (nested under field).

### Protocol and Transport Options

Thrift supports two wire protocols and two transport layers:

```yaml
protocol:
  type: "http_to_thrift"
  thrift:
    idl_file: "/etc/idl/service.thrift"
    service: "MyService"
    protocol: "compact"    # "binary" (default) or "compact"
    transport: "framed"    # "framed" (default) or "buffered"
```

### Multiplexed Services

When the backend uses `TMultiplexedProtocol` (multiple services on a single port), enable the `multiplexed` flag:

```yaml
protocol:
  type: "http_to_thrift"
  thrift:
    idl_file: "/etc/idl/service.thrift"
    service: "UserService"
    multiplexed: true
```

This prepends `ServiceName:` to the method name on the wire (e.g., `UserService:GetUser`).

### TLS to Thrift Backend

```yaml
protocol:
  type: "http_to_thrift"
  thrift:
    idl_file: "/etc/idl/service.thrift"
    service: "MyService"
    tls:
      enabled: true
      cert_file: "/etc/certs/client.crt"   # optional mTLS
      key_file: "/etc/certs/client.key"     # optional mTLS
      ca_file: "/etc/certs/ca.crt"          # required
```

### Oneway Methods

Thrift `oneway` methods (fire-and-forget) are supported. The gateway sends the call but does not wait for a response, immediately returning `{}` with HTTP 200 to the client.

### Type Mapping

| Thrift Type | JSON Representation |
|-------------|-------------------|
| bool | `true` / `false` |
| byte, i16, i32, i64 | number |
| double | number |
| string | string |
| binary | base64-encoded string |
| enum | string name or numeric value |
| struct/union/exception | object |
| list/set | array |
| map | object (string keys) |

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
| `protocol.type` | string | `http_to_grpc` or `http_to_thrift` |
| `protocol.grpc.service` | string | Fully-qualified gRPC service name |
| `protocol.grpc.timeout` | duration | Per-call timeout (default 30s) |
| `protocol.grpc.descriptor_cache_ttl` | duration | Reflection cache TTL (default 5m) |
| `protocol.grpc.mappings` | []GRPCMethodMapping | REST-to-gRPC path mappings |
| `protocol.thrift.idl_file` | string | Path to `.thrift` IDL file (mutually exclusive with `methods`) |
| `protocol.thrift.service` | string | Thrift service name (required) |
| `protocol.thrift.method` | string | Fixed method name (optional) |
| `protocol.thrift.timeout` | duration | Per-call timeout (default 30s) |
| `protocol.thrift.protocol` | string | `binary` (default) or `compact` |
| `protocol.thrift.transport` | string | `framed` (default) or `buffered` |
| `protocol.thrift.multiplexed` | bool | Enable TMultiplexedProtocol |
| `protocol.thrift.mappings` | []ThriftMethodMapping | REST-to-Thrift path mappings |
| `protocol.thrift.methods` | map[string]ThriftMethodDef | Inline method schemas (mutually exclusive with `idl_file`) |
| `protocol.thrift.structs` | map[string][]ThriftFieldDef | Inline struct definitions |
| `protocol.thrift.enums` | map[string]map[string]int | Inline enum definitions |
| `grpc.enabled` | bool | Enable gRPC passthrough (mutually exclusive with protocol) |
| `websocket.enabled` | bool | Enable WebSocket proxying |
| `websocket.ping_interval` | duration | Keep-alive ping interval |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
