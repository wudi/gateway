# gRPC-Web Proxy

The gateway can proxy gRPC-Web requests from browser clients to native gRPC backends. Unlike `http_to_grpc` (which converts JSON to protobuf), gRPC-Web proxy passes protobuf bytes through unchanged — it only transforms the framing layer.

## What is gRPC-Web?

gRPC-Web is a protocol that allows browser-based JavaScript clients to call gRPC services. Browsers cannot use native gRPC (which requires HTTP/2 trailers), so gRPC-Web encodes trailers in the response body using a length-prefixed framing format. The gateway handles this translation transparently.

## Configuration

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
        max_message_size: 4194304  # 4MB
        text_mode: true
    cors:
      enabled: true
      allowed_origins: ["https://app.example.com"]
      allowed_methods: ["POST"]
      allowed_headers: ["Content-Type", "X-Grpc-Web", "X-User-Agent"]
      exposed_headers: ["Grpc-Status", "Grpc-Message"]
```

Browser clients send requests to `/mypackage.MyService/MethodName` with content type `application/grpc-web+proto` (binary) or `application/grpc-web-text+proto` (base64-encoded text mode).

## Supported Content Types

| Content Type | Encoding | Description |
|---|---|---|
| `application/grpc-web` | Binary | Standard gRPC-Web binary format |
| `application/grpc-web+proto` | Binary | Explicit protobuf sub-type |
| `application/grpc-web-text` | Base64 | Text mode for environments without binary support |
| `application/grpc-web-text+proto` | Base64 | Text mode with explicit protobuf sub-type |

## Supported RPC Modes

- **Unary**: Single request, single response. Fully supported.
- **Server streaming**: Not yet supported (planned).
- **Client streaming**: Not supported by the gRPC-Web specification.
- **Bidirectional streaming**: Not supported by the gRPC-Web specification.

## Text Mode (Base64)

When `text_mode: true` (the default), the gateway accepts `application/grpc-web-text` requests where the body is base64-encoded. Each response frame is independently base64-encoded, allowing progressive decoding by the client. Set `text_mode: false` to reject text-mode requests.

## CORS

Browser gRPC-Web clients send cross-origin requests. Configure CORS on the route using the gateway's built-in CORS middleware — the translator does not handle CORS itself.

```yaml
cors:
  enabled: true
  allowed_origins: ["https://app.example.com"]
  allowed_methods: ["POST"]
  allowed_headers: ["Content-Type", "X-Grpc-Web", "X-User-Agent"]
  exposed_headers: ["Grpc-Status", "Grpc-Message"]
```

## TLS to Backend

When the gRPC backend requires TLS:

```yaml
protocol:
  type: "grpc_web"
  grpc_web:
    timeout: 30s
    tls:
      enabled: true
      ca_file: "/etc/certs/grpc-ca.crt"          # required
      cert_file: "/etc/certs/grpc-client.crt"     # optional mTLS
      key_file: "/etc/certs/grpc-client.key"       # optional mTLS
```

## Comparison with http_to_grpc

| Feature | `grpc_web` | `http_to_grpc` |
|---|---|---|
| Input format | Protobuf (gRPC-Web wire format) | JSON |
| Output format | Protobuf (gRPC-Web wire format) | JSON |
| Reflection needed | No | Yes |
| Descriptor cache | No | Yes |
| REST mappings | No (uses gRPC path directly) | Yes |
| Use case | Browser gRPC-Web clients | REST-to-gRPC translation |

## Wire Format

gRPC-Web frames use a 5-byte header: 1 byte flag + 4 bytes big-endian length, followed by the payload.

- **Data frame** (flag `0x00`): Contains the protobuf message bytes.
- **Trailer frame** (flag `0x80`): Contains `key: value\r\n` pairs (e.g., `grpc-status: 0\r\n`).

A typical response contains one data frame followed by one trailer frame. Error responses may be trailer-only (no data frame).

## Admin API

gRPC-Web routes appear in the existing protocol translator admin endpoint:

```
GET /protocol-translators
```

Response includes per-route metrics:

```json
{
  "grpc-web-api": {
    "requests": 150,
    "successes": 148,
    "failures": 2,
    "avg_latency_ms": 12.5,
    "protocol_type": "grpc_web"
  }
}
```

## Config Reference

| Field | Type | Default | Description |
|---|---|---|---|
| `protocol.grpc_web.timeout` | duration | `30s` | Per-call timeout for gRPC backend |
| `protocol.grpc_web.max_message_size` | int | `4194304` (4MB) | Maximum message size in bytes |
| `protocol.grpc_web.text_mode` | bool | `true` | Accept `grpc-web-text` base64 encoding |
| `protocol.grpc_web.tls.enabled` | bool | `false` | Enable TLS to gRPC backend |
| `protocol.grpc_web.tls.ca_file` | string | | CA certificate (required when TLS enabled) |
| `protocol.grpc_web.tls.cert_file` | string | | Client certificate (optional mTLS) |
| `protocol.grpc_web.tls.key_file` | string | | Client key (optional mTLS) |
