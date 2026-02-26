---
title: "Idempotency Key Support"
sidebar_position: 7
---

Idempotency key support prevents duplicate processing of mutation requests (POST, PUT, PATCH). Clients include an `Idempotency-Key` header; the runway stores the full response and replays it on duplicate keys, ensuring that retried or duplicated requests never result in double-processing.

This pattern is widely used in financial APIs (Stripe, PayPal) for payments, orders, and any operation that must be processed exactly once.

## How It Works

1. Client sends a request with `Idempotency-Key: <unique-id>` header
2. Runway checks if this key has been seen before:
   - **New key**: Request proceeds to backend; response is captured and stored
   - **Duplicate key (stored)**: Stored response is replayed immediately (adds `X-Idempotent-Replayed: true`)
   - **Duplicate key (in-flight)**: Waits for the original request to complete, then replays its response
3. Stored responses expire after the configured TTL (default: 24 hours)

## Configuration

### Global Configuration

```yaml
idempotency:
  enabled: true
  header_name: "Idempotency-Key"   # default
  ttl: 24h                          # how long to store responses
  methods:                           # which HTTP methods to check
    - POST
    - PUT
    - PATCH
  enforce: false                     # reject mutations without a key (422)
  key_scope: "global"                # "global" or "per_client"
  mode: "local"                      # "local" or "distributed"
  max_key_length: 256                # maximum key length (400 if exceeded)
  max_body_size: 1048576             # max response body to store (1MB)
```

### Per-Route Configuration

Per-route settings override global settings:

```yaml
routes:
  - id: payments
    path: /api/payments
    idempotency:
      enabled: true
      enforce: true            # require idempotency key for this route
      ttl: 48h                 # longer TTL for payment responses
      key_scope: per_client    # scope keys per authenticated client
```

## Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable idempotency key checking |
| `header_name` | string | `Idempotency-Key` | Header name to read the key from |
| `ttl` | duration | `24h` | How long stored responses are kept |
| `methods` | list | `[POST, PUT, PATCH]` | HTTP methods to apply idempotency checking to |
| `enforce` | bool | `false` | Reject requests without an idempotency key with 422 |
| `key_scope` | string | `global` | `global` = keys shared across all clients; `per_client` = keys scoped by authenticated client ID |
| `mode` | string | `local` | `local` = in-memory storage; `distributed` = Redis-backed (requires `redis.address`) |
| `max_key_length` | int | `256` | Maximum allowed key length; longer keys get 400 |
| `max_body_size` | int64 | `1048576` | Maximum response body size to store (bytes); larger responses are not cached |

## Key Scoping

### Global Scope (default)

All clients share the same key namespace. If client A sends `Idempotency-Key: abc123`, client B cannot reuse the same key.

### Per-Client Scope

Keys are prefixed with the authenticated client's ID (`Identity.ClientID`). This requires authentication to be configured. Each client has an independent key namespace.

```yaml
idempotency:
  enabled: true
  key_scope: per_client
```

## Storage Modes

### Local (default)

Responses are stored in-memory with a background cleanup goroutine. Suitable for single-instance deployments.

### Distributed

Responses are stored in Redis using gob serialization. Keys are stored as `gw:idem:{route_id}:{key}` with the configured TTL. Redis errors are handled with a fail-open policy (request proceeds to backend).

```yaml
redis:
  address: "localhost:6379"

idempotency:
  enabled: true
  mode: distributed
```

## In-Flight Deduplication

When a duplicate key arrives while the original request is still being processed:

1. The duplicate request blocks and waits for the original to complete
2. When the original completes, its response is shared with all waiting duplicates
3. If the client's context is cancelled (timeout/disconnect), the wait is abandoned
4. If the original request fails without storing a response (`CancelInFlight`), waiting requests proceed independently

## Response Headers

Replayed responses include:

```
X-Idempotent-Replayed: true
```

This header allows clients to distinguish between original and replayed responses.

## Error Responses

| Status | Condition |
|--------|-----------|
| 422 | `enforce: true` and request has no `Idempotency-Key` header |
| 400 | Key exceeds `max_key_length` |

## Middleware Chain Position

The idempotency middleware runs at **step 6.4** in the middleware chain:

- After authentication, external auth, nonce, and CSRF (so `per_client` scoping can use `Identity.ClientID`)
- Before priority admission, rules, and proxy (cached responses skip all heavy processing)

## Admin API

### GET /idempotency

Returns per-route idempotency statistics:

```json
{
  "payments": {
    "header_name": "Idempotency-Key",
    "ttl": "24h0m0s",
    "enforce": true,
    "key_scope": "per_client",
    "mode": "local",
    "total_requests": 1500,
    "cache_hits": 45,
    "cache_misses": 1455,
    "in_flight_waits": 3,
    "enforced": 12,
    "invalid_key": 0,
    "store_errors": 0,
    "responses_stored": 1455
  }
}
```

## Examples

### Payment API with Enforcement

```yaml
idempotency:
  enabled: true
  enforce: true
  key_scope: per_client
  ttl: 48h
  mode: distributed

redis:
  address: "redis:6379"

routes:
  - id: create-payment
    path: /api/v1/payments
    methods: [POST]
    backends:
      - url: http://payment-service:8080
    idempotency:
      enabled: true
      enforce: true
```

### Order API with Custom Header

```yaml
routes:
  - id: create-order
    path: /api/v1/orders
    methods: [POST, PUT]
    backends:
      - url: http://order-service:8080
    idempotency:
      enabled: true
      header_name: "X-Request-Id"
      methods: [POST]
      ttl: 12h
```
