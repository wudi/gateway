---
title: "Raw Body Passthrough"
sidebar_position: 13
---

Skip all body-processing middleware for routes that handle binary protocols or need zero overhead. When `passthrough: true`, the request and response bodies are forwarded without inspection or transformation.

## Configuration

```yaml
routes:
  - id: binary-api
    path: /binary/
    path_prefix: true
    backends:
      - url: http://backend:8080
    passthrough: true
```

## What Gets Skipped

The following middleware steps are bypassed in passthrough mode:

| Step | Middleware | Reason |
|------|-----------|--------|
| 8 | Body size limit | No body inspection |
| 8.25 | Request decompression | No body processing |
| 8.5 | Bandwidth limiting | No body wrapping |
| 9 | Request validation | No body schema check |
| 9.1 | OpenAPI request validation | No body schema check |
| 9.5 | GraphQL analysis | No query parsing |
| 11 | Cache | No body buffering |
| 11.5 | Request coalescing | No response sharing |
| 13 | Compression | No body encoding |
| 13.5 | Response size limit | No body inspection |
| 16 (body part) | Request body transform | No body modification |
| 17 | Response body transform | No body modification |
| 17.5 | Response validation | No body schema check |

## What Still Runs

All non-body middleware still executes:

- Metrics, IP filtering, geo filtering, maintenance mode
- CORS, security headers, error pages, access logging
- Rate limiting, throttling, auth, CSRF, nonces
- Request rules, WAF, fault injection
- Circuit breaker, outlier detection, adaptive concurrency
- Header transforms (step 16 still runs for headers)
- Response rules, mirroring, traffic groups
- Backend auth, backend signing
- Status mapping

## Mutual Exclusions

`passthrough` cannot be combined with:
- Body transforms (`transform.request.body` or `transform.response.body`)
- `validation.enabled`
- `compression.enabled`
- `cache.enabled`
- `graphql.enabled`
- `openapi.spec_file` or `openapi.spec_id`
- `request_decompression.enabled`
- `response_limit.enabled`

These are enforced by config validation.

## Use Cases

- Binary protocol proxying (gRPC without transcoding, Thrift, protobuf)
- File upload/download routes where body inspection adds unnecessary overhead
- WebSocket-heavy routes (WebSocket upgrade itself already bypasses most body middleware)
- Streaming routes where buffering is unacceptable
