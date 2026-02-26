---
title: "Response Size Limiting"
sidebar_position: 12
---

Response size limiting protects the runway from oversized backend responses that could exhaust memory or bandwidth. It enforces a maximum response body size from upstream backends on a per-route or global basis.

## Configuration

### Global

```yaml
response_limit:
  enabled: true
  max_size: 1048576    # 1MB
  action: reject       # "reject", "truncate", or "log_only"
```

### Per-Route

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    response_limit:
      enabled: true
      max_size: 5242880   # 5MB
      action: truncate
```

Per-route settings override global settings. Non-zero per-route fields take precedence.

## Actions

### `reject` (default)

- If the backend response includes a `Content-Length` header exceeding `max_size`, the runway immediately returns **502 Bad Gateway** to the client without forwarding any response body.
- For streaming/chunked responses (no `Content-Length`), the runway writes bytes until the limit is reached, then silently discards remaining data.
- Sets `X-Response-Limited: true` header when limiting occurs.

### `truncate`

- Writes up to `max_size` bytes of the response body, then discards the rest.
- The client receives a truncated response with whatever data was written before the limit.
- Useful when partial data is acceptable (e.g., large JSON arrays where initial elements are sufficient).

### `log_only`

- Passes the full response through without modification.
- Increments the `limited` metric counter when the response exceeds `max_size`.
- Use this to monitor response sizes before enforcing limits in production.

## Middleware Position

Response size limiting runs at step **13.5** in the middleware chain, between compression (13) and response rules (14). In the response flow (backend to client), the limiter sees the raw uncompressed response from the backend before compression reduces its size.

## Metrics

Available via the admin API at `GET /response-limits`:

| Field | Description |
|-------|-------------|
| `total_responses` | Total number of responses processed |
| `limited` | Number of responses that exceeded the limit |
| `total_bytes` | Total response bytes observed |
| `max_size` | Configured maximum size in bytes |
| `action` | Configured action (reject/truncate/log_only) |

## Examples

### Protect APIs from oversized responses

```yaml
response_limit:
  enabled: true
  max_size: 10485760   # 10MB global default
  action: reject

routes:
  - id: api
    path: /api
    backends:
      - url: http://api-backend:8080
    # Inherits global 10MB limit

  - id: files
    path: /files
    backends:
      - url: http://file-server:8080
    response_limit:
      enabled: true
      max_size: 104857600  # 100MB for file downloads
      action: truncate
```

### Monitor before enforcing

```yaml
response_limit:
  enabled: true
  max_size: 5242880    # 5MB
  action: log_only     # monitor first, then switch to reject
```

Check `GET /response-limits` to see how many responses would be affected, then change to `reject` when ready.
