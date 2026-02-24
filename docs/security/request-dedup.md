---
title: "Request Deduplication"
sidebar_position: 11
---

Request deduplication detects and collapses duplicate requests based on content fingerprinting. When an identical request arrives while a previous one is still in-flight, the duplicate waits and receives the same response. Already-completed responses are replayed from a short-lived cache. This is useful for webhook receivers and APIs that may receive duplicate deliveries.

## Configuration

Request dedup is configured per-route.

```yaml
routes:
  - id: webhook-receiver
    path: /webhooks
    request_dedup:
      enabled: true
      ttl: 60s                  # how long to cache responses (default 60s)
      include_headers:          # headers to include in fingerprint
        - X-Webhook-ID
        - X-Delivery-ID
      include_body: true        # include request body in fingerprint (default true)
      max_body_size: 1048576    # max body bytes to hash (default 1MB)
      mode: local               # "local" or "distributed" (default "local")
```

### Distributed Mode

Use `mode: distributed` with Redis to share dedup state across gateway instances:

```yaml
redis:
  address: "localhost:6379"

routes:
  - id: webhook-receiver
    path: /webhooks
    request_dedup:
      enabled: true
      mode: distributed
      ttl: 120s
```

## How It Works

1. A SHA-256 fingerprint is computed from: HTTP method + path + query string + sorted configured header values + request body (up to `max_body_size`)
2. The fingerprint is checked against the response store (memory or Redis)
3. If a stored response exists and hasn't expired, it is replayed immediately with `X-Dedup-Replayed: true` header
4. If an identical request is currently in-flight, the new request waits for it to complete and receives the same response
5. Otherwise the request proceeds normally; its response is captured and stored for the TTL duration

## Fingerprinting

The fingerprint includes:
- HTTP method (GET, POST, etc.)
- Request path
- Query string (sorted by key)
- Values of headers listed in `include_headers` (sorted by header name)
- Request body (when `include_body: true`, limited to `max_body_size` bytes)

The request body is fully read for fingerprinting, then restored so downstream handlers receive it unchanged.

## Middleware Position

Request dedup runs at step **6.45** in the middleware chain — after idempotency (6.4) and before priority admission (6.5).

## Admin API

### GET `/request-dedup`

Returns dedup status for all routes.

```bash
curl http://localhost:8081/request-dedup
```

**Response (200 OK):**

```json
{
  "webhook-receiver": {
    "enabled": true,
    "mode": "local",
    "ttl": "1m0s",
    "include_headers": ["X-Webhook-ID", "X-Delivery-ID"],
    "include_body": true,
    "max_body_size": 1048576
  }
}
```

## Validation

- `mode` must be `"local"` or `"distributed"`
- `mode: distributed` requires `redis.address` to be configured
- `ttl` must be >= 0 (0 uses the default of 60s)
- `max_body_size` must be >= 0 (0 uses the default of 1MB)
