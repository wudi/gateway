# ETag Generation

The ETag middleware generates ETag headers from response bodies, enabling conditional requests and bandwidth savings via `304 Not Modified` responses.

## Configuration

Per-route:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    etag:
      enabled: true
      weak: false
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `etag.enabled` | bool | false | Enable ETag generation |
| `etag.weak` | bool | false | Use weak validators (prefix `W/`) |

## How It Works

1. The middleware buffers the response body and computes a SHA-256 hash.
2. The hash is set as the `ETag` response header. When `weak: true`, the value is prefixed with `W/` for weak validation.
3. If the request includes an `If-None-Match` header matching the computed ETag, the middleware returns `304 Not Modified` with no body.

## Mutual Exclusivity

The standalone `etag` middleware is mutually exclusive with `cache.conditional`. The cache middleware has its own built-in conditional request support -- use `cache.conditional: true` when caching is enabled, and `etag.enabled: true` when you want ETag generation without caching.

## Admin Endpoint

`GET /etag` returns per-route ETag generation statistics.

```bash
curl http://localhost:8081/etag
```

See [Configuration Reference](configuration-reference.md#etag-per-route) for field details.
