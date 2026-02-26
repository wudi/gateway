---
title: "Replay Prevention"
sidebar_position: 8
---

The runway provides nonce-based replay prevention to protect against request replay attacks. Clients include a unique `X-Nonce` header (configurable) with each request, and the runway rejects any request carrying a previously-seen nonce within a configurable TTL window.

## How It Works

1. Client sends a request with a unique nonce value in the `X-Nonce` header
2. Runway checks if this nonce has been seen before within the TTL window
3. If new: request proceeds, nonce is stored
4. If duplicate: request is rejected with `409 Conflict`
5. If missing and `required: true`: request is rejected with `400 Bad Request`

## Configuration

Nonce checking can be configured globally and per route. Per-route settings override global settings field by field.

### Global

```yaml
nonce:
  enabled: true
  header: "X-Nonce"          # header name (default "X-Nonce")
  query_param: ""            # optional query parameter name (e.g. "nonce")
  ttl: 5m                    # how long nonces are remembered (default 5m)
  mode: "local"              # "local" (default) or "distributed"
  scope: "global"            # "global" (default) or "per_client"
  required: true             # reject requests without nonce (default true)
  timestamp_header: ""       # optional timestamp header for age validation
  max_age: 0                 # max request age (requires timestamp_header)
```

### Per-Route

```yaml
routes:
  - id: "payments"
    path: "/api/payments"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    nonce:
      enabled: true
      ttl: 10m
      scope: "per_client"
      required: true
```

Per-route nonce config is merged with the global config. Any field set on the per-route config overrides the corresponding global field. If only the global config is enabled, all routes use global settings.

## Nonce Source

By default, the nonce is read from the request header (`X-Nonce`). You can also configure a query parameter as a fallback source using `query_param`. When both are configured, the header takes precedence — the query parameter is only checked if the header is absent.

```yaml
nonce:
  enabled: true
  header: "X-Nonce"
  query_param: "nonce"       # fallback: read from ?nonce=<value>
```

This allows clients to pass the nonce via URL when headers are inconvenient (e.g., browser redirects, webhook callbacks):

```
GET /api/callback?nonce=abc123&token=xyz
```

You can also use `query_param` alone by leaving `header` at its default — the header is checked first, and the query parameter is used only when the header is empty.

## Storage Modes

### Local (default)

Nonces are stored in-memory per runway instance. Fast and zero-dependency, but nonces seen by one instance are not visible to others. Suitable for single-instance deployments or when clients are sticky to a runway instance.

### Distributed

Nonces are stored in Redis using atomic `SET NX PX` operations. All runway instances share the same nonce store, preventing replays across instances.

```yaml
redis:
  address: "localhost:6379"

nonce:
  enabled: true
  mode: "distributed"
  ttl: 5m
```

Redis key pattern: `gw:nonce:{routeID}:{nonceKey}`

On Redis errors, the runway **fails open** (allows the request and logs a warning) to avoid blocking traffic due to Redis outages.

## Scope

### Global (default)

The nonce value alone is the key. A nonce `abc123` can only be used once by any client.

### Per-Client

The key is `{clientID}:{nonce}`, where `clientID` comes from the authenticated identity (`variables.Identity.ClientID`) with fallback to the client IP. Different clients can reuse the same nonce value.

```yaml
nonce:
  enabled: true
  scope: "per_client"
```

## Timestamp Validation

Optionally validate request freshness via a timestamp header. Requests older than `max_age` are rejected with `400 Bad Request` before the nonce check.

```yaml
nonce:
  enabled: true
  timestamp_header: "X-Timestamp"
  max_age: 30s
```

The timestamp header value is parsed as RFC 3339 (`2026-01-15T10:30:00Z`) or Unix seconds (`1737014400`).

## Middleware Chain Position

The nonce middleware runs at position **6.3** in the middleware chain — after authentication (so `Identity.ClientID` is available for `per_client` scope) and before priority admission:

```
... → 6. authMW → 6.25. extAuthMW → 6.3. nonceMW → 6.5. priorityMW → ...
```

## Response Codes

| Code | Meaning |
|------|---------|
| `400` | Missing nonce header (when `required: true`) or invalid/expired timestamp |
| `409` | Duplicate nonce (replay detected) |

Responses are JSON:

```json
{"error": "replay detected"}
```

## Admin API

### GET `/nonces`

Returns nonce checker status and metrics per route.

```bash
curl http://localhost:8081/nonces
```

**Response:**
```json
{
  "payments": {
    "header": "X-Nonce",
    "mode": "local",
    "scope": "per_client",
    "ttl": "5m0s",
    "required": true,
    "metrics": {
      "total_checked": 1500,
      "rejected": 3,
      "missing_nonce": 12,
      "stale_timestamp": 0,
      "store_size": 847
    }
  }
}
```

## Example: Payment API

```yaml
nonce:
  enabled: true
  header: "X-Idempotency-Key"
  ttl: 24h
  mode: "distributed"
  scope: "per_client"
  required: true

redis:
  address: "redis:6379"

routes:
  - id: "create-payment"
    path: "/api/v1/payments"
    methods: ["POST"]
    backends:
      - url: "http://payments:8080"
    nonce:
      enabled: true
```

Client usage:

```bash
curl -X POST https://runway.example.com/api/v1/payments \
  -H "Authorization: Bearer <token>" \
  -H "X-Idempotency-Key: pay_$(uuidgen)" \
  -H "Content-Type: application/json" \
  -d '{"amount": 1000, "currency": "USD"}'
```

Retrying the same request with the same `X-Idempotency-Key` within 24 hours returns `409 Conflict`.
