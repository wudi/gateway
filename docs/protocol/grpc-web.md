---
title: "gRPC-Web Proxy"
sidebar_position: 6
---

The runway can proxy gRPC-Web requests from browser clients to native gRPC backends. Unlike `http_to_grpc` (which converts JSON to protobuf), gRPC-Web proxy passes protobuf bytes through unchanged — it only transforms the framing layer.

## What is gRPC-Web?

gRPC-Web is a protocol that allows browser-based JavaScript clients to call gRPC services. Browsers cannot use native gRPC (which requires HTTP/2 trailers), so gRPC-Web encodes trailers in the response body using a length-prefixed framing format. The runway handles this translation transparently.

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
- **Server streaming**: Single request, multiple responses streamed to the client. Fully supported.
- **Client streaming**: Not supported by the gRPC-Web specification.
- **Bidirectional streaming**: Not supported by the gRPC-Web specification.

## Server Streaming

Server streaming allows a single request to receive multiple response messages streamed in real time. The client signals streaming intent via a query parameter or header — there is no wire-level difference between unary and server-streaming gRPC-Web requests.

### Signaling Streaming Mode

Use either method (or both):

- **Query parameter**: `?streaming=server`
- **Header**: `X-Grpc-Web-Streaming: server`

If neither is present, the request is treated as unary (fully backward compatible).

### Example

```
POST /mypackage.MyService/ListItems?streaming=server HTTP/1.1
Content-Type: application/grpc-web+proto
```

Or with header:

```
POST /mypackage.MyService/ListItems HTTP/1.1
Content-Type: application/grpc-web+proto
X-Grpc-Web-Streaming: server
```

### Streaming Response Format

The response is a sequence of gRPC-Web frames, each flushed to the client as it arrives from the backend:

1. **HTTP 200** with `Content-Type: application/grpc-web+proto` (or `application/grpc-web-text+proto` for text mode)
2. **N data frames** (flag `0x00`) — one per response message from the backend
3. **1 trailer frame** (flag `0x80`) — contains `grpc-status: 0` on success, or an error status with `grpc-message`

In text mode (`application/grpc-web-text+proto`), each frame is independently base64-encoded before being written, allowing the client to progressively decode as frames arrive.

### Configuration

No additional configuration is needed. Server streaming uses the same route config as unary — the `timeout`, `max_message_size`, and `text_mode` settings apply equally to streaming RPCs.

## Text Mode (Base64)

When `text_mode: true` (the default), the runway accepts `application/grpc-web-text` requests where the body is base64-encoded. Each response frame is independently base64-encoded, allowing progressive decoding by the client. Set `text_mode: false` to reject text-mode requests.

## CORS

Browser gRPC-Web clients send cross-origin requests. Configure CORS on the route using the runway's built-in CORS middleware — the translator does not handle CORS itself.

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
