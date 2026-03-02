---
title: "Protocol Translation"
sidebar_position: 1
---

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

## gRPC-Web Proxy

Proxies gRPC-Web requests from browser clients to native gRPC backends. Unlike `http_to_grpc`, this passes protobuf bytes through unchanged — only the framing layer is transformed (gRPC-Web wire format to native gRPC).

```yaml
routes:
  - id: "grpc-web-api"
    path: "/mypackage.MyService/*method"
    path_prefix: true
    backends:
      - url: "grpc://grpc-backend:50051"
    protocol:
      type: "grpc_web"
      grpc_web:
        timeout: 30s
        max_message_size: 4194304
        text_mode: true
    cors:
      enabled: true
      allowed_origins: ["https://app.example.com"]
      allowed_methods: ["POST"]
      allowed_headers: ["Content-Type", "X-Grpc-Web"]
```

Browser clients send requests with content type `application/grpc-web+proto` (binary) or `application/grpc-web-text+proto` (base64 text mode). Both unary and server-streaming RPCs are supported — the client signals streaming with `?streaming=server` or `X-Grpc-Web-Streaming: server`. CORS is handled by the gateway's CORS middleware, not the translator.

See [gRPC-Web Proxy](grpc-web.md) for full documentation.

## HTTP-to-gRPC JSON Codec Proxy

Proxies HTTP/JSON requests to gRPC backends using a custom JSON codec (`content-type: application/grpc+json`). Both the gateway and the backend send raw JSON bytes over the gRPC wire — **no proto descriptors or server reflection required**.

> **Backend requirement:** The backend gRPC server **must** register a codec named `"json"` via `encoding.RegisterCodec()`. Without this, calls fail with `Unimplemented` (codec not found). See [Backend Setup](#backend-setup) below.

### When to Use

- You **control the backend** and can register a JSON codec
- You want the simplest possible gRPC integration (no `.proto` files, no reflection)
- Your backend already accepts JSON-encoded gRPC requests

### When NOT to Use

- Third-party gRPC backends that only speak protobuf — use `http_to_grpc` instead
- Browser clients that need gRPC-Web framing — use `grpc_web` instead

### Path-Based Mode (Default)

The URL path is interpreted as the gRPC method path (`/package.Service/Method`):

```yaml
routes:
  - id: "grpc-json-api"
    path: "/mypackage.MyService"
    path_prefix: true
    backends:
      - url: "grpc://grpc-backend:50051"
    protocol:
      type: "grpc_json"
      grpc_json:
        timeout: 30s
```

A POST to `/mypackage.MyService/GetUser` invokes `mypackage.MyService.GetUser`.

### Service-Scoped Mode

Fix the service name; the method is extracted from the last URL path segment:

```yaml
routes:
  - id: "grpc-json-scoped"
    path: "/api/myservice"
    path_prefix: true
    backends:
      - url: "grpc://grpc-backend:50051"
    protocol:
      type: "grpc_json"
      grpc_json:
        service: "mypackage.MyService"
        timeout: 30s
```

A POST to `/api/myservice/GetUser` invokes `mypackage.MyService.GetUser`.

### Fixed Method Mode

Always invoke the same RPC regardless of URL path:

```yaml
routes:
  - id: "grpc-json-fixed"
    path: "/get-user"
    backends:
      - url: "grpc://grpc-backend:50051"
    protocol:
      type: "grpc_json"
      grpc_json:
        service: "mypackage.UserService"
        method: "GetUser"
        timeout: 30s
```

### TLS Configuration

```yaml
protocol:
  type: "grpc_json"
  grpc_json:
    timeout: 30s
    tls:
      enabled: true
      ca_file: "/etc/certs/ca.pem"
      cert_file: "/etc/certs/client.pem"
      key_file: "/etc/certs/client-key.pem"
```

### Backend Setup

The backend gRPC server must register a JSON codec. Example in Go:

```go
import "google.golang.org/grpc/encoding"

type jsonCodec struct{}

func (jsonCodec) Marshal(v interface{}) ([]byte, error)     { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v interface{}) error { return json.Unmarshal(data, v) }
func (jsonCodec) Name() string                               { return "json" }

func init() {
    encoding.RegisterCodec(jsonCodec{})
}
```

### How It Works

1. The gateway receives an HTTP POST with a JSON body
2. The JSON body is read as raw bytes (no parsing or transformation)
3. A gRPC connection is established with `ForceCodec(jsonCodec{})`, which sets the wire content-type to `application/grpc+json`
4. The raw JSON bytes are sent via `conn.Invoke()` using gRPC framing
5. The backend's JSON codec deserializes the bytes into the target proto message
6. The response follows the reverse path: proto message → JSON codec → raw bytes → HTTP response

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `protocol.type` | string | `http_to_grpc`, `http_to_thrift`, `grpc_to_rest`, `grpc_web`, or `grpc_json` |
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
| `protocol.rest.timeout` | duration | Per-call timeout (default 30s) |
| `protocol.rest.descriptor_files` | []string | Paths to `.pb` descriptor set files |
| `protocol.rest.mappings` | []GRPCToRESTMapping | gRPC method → REST endpoint mappings (required) |
| `protocol.grpc_web.timeout` | duration | Per-call timeout (default 30s) |
| `protocol.grpc_web.max_message_size` | int | Maximum message size in bytes (default 4MB) |
| `protocol.grpc_web.text_mode` | bool | Accept grpc-web-text base64 encoding |
| `protocol.grpc_json.service` | string | Fully-qualified gRPC service name (optional) |
| `protocol.grpc_json.method` | string | Fixed gRPC method name (requires `service`) |
| `protocol.grpc_json.timeout` | duration | Per-call timeout (default 30s) |
| `protocol.grpc_json.tls` | object | TLS settings for backend connection |
| `grpc.enabled` | bool | Enable gRPC passthrough (mutually exclusive with protocol) |
| `websocket.enabled` | bool | Enable WebSocket proxying |
| `websocket.ping_interval` | duration | Keep-alive ping interval |

See [Configuration Reference](../reference/configuration-reference.md#routes) for all fields.

## gRPC-to-REST Translation

Translates incoming gRPC requests (`application/grpc`) to REST/HTTP backend calls. This is the reverse of HTTP-to-gRPC: it accepts native gRPC clients and forwards to REST APIs.

### Configuration

```yaml
routes:
  - id: "grpc-gateway"
    path: "/users.UserService/*"
    backends:
      - url: "http://rest-api:8080"
    protocol:
      type: "grpc_to_rest"
      rest:
        timeout: 30s
        descriptor_files:
          - /etc/proto/service.pb
        mappings:
          - grpc_service: "users.UserService"
            grpc_method: "GetUser"
            http_method: "GET"
            http_path: "/api/users/{user_id}"
            body: ""
          - grpc_service: "users.UserService"
            grpc_method: "CreateUser"
            http_method: "POST"
            http_path: "/api/users"
            body: "*"
          - grpc_service: "users.UserService"
            grpc_method: "UpdateUser"
            http_method: "PUT"
            http_path: "/api/users/{user_id}"
            body: "*"
          - grpc_service: "users.UserService"
            grpc_method: "DeleteUser"
            http_method: "DELETE"
            http_path: "/api/users/{user_id}"
            body: ""
```

### How It Works

1. The gateway receives a gRPC request (Content-Type: `application/grpc`)
2. The gRPC path (`/users.UserService/GetUser`) is matched against configured mappings
3. The gRPC wire-format body (5-byte header + protobuf/JSON payload) is decoded
4. If descriptor files are loaded, the protobuf message is properly unmarshaled to JSON via `protojson`
5. Path template variables (e.g., `{user_id}`) are substituted from the request message fields
6. A REST request is built with the configured HTTP method, path, and JSON body
7. The REST backend response is converted back to a gRPC wire-format response

### Descriptor Files

For proper protobuf ↔ JSON conversion, provide pre-compiled `.pb` descriptor set files. Generate them with:

```bash
protoc --descriptor_set_out=service.pb --include_imports service.proto
```

Without descriptor files, the translator operates in "JSON passthrough" mode: it treats the gRPC body as raw JSON and passes it through, which works with gRPC-web JSON encoding but not with standard protobuf-encoded gRPC.

### Mapping Fields

| Field | Type | Description |
|-------|------|-------------|
| `grpc_service` | string | Fully-qualified gRPC service name (required) |
| `grpc_method` | string | gRPC method name (required) |
| `http_method` | string | HTTP method: GET, POST, PUT, DELETE, PATCH (required) |
| `http_path` | string | REST path with template variables (required) |
| `body` | string | `"*"` = send full body as JSON, `""` = no body (query params only) |

### Path Templates

Path templates use `{field_name}` syntax to extract values from the gRPC request message:

- `/users/{user_id}` — extracts `user_id` from the message
- `/orgs/{org_id}/users/{user_id}` — extracts both `org_id` and `user_id`

When `body: "*"`, fields used as path variables are stripped from the request body to avoid duplication.

### Validation Rules

- At least one mapping is required
- Each mapping must have `grpc_service`, `grpc_method`, `http_method`, and `http_path`
- `http_method` must be one of: GET, POST, PUT, DELETE, PATCH
- Duplicate gRPC service/method combinations are not allowed
- `grpc_to_rest` is mutually exclusive with `grpc.enabled`
