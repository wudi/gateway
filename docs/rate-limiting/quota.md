---
title: "Quota Enforcement"
sidebar_position: 4
---

Quota enforcement tracks per-client usage over billing periods (hourly, daily, monthly, yearly). Unlike rate limiting which controls short-term burst, quotas enforce long-term usage caps aligned to calendar boundaries.

## How It Works

Each request increments a counter keyed by client identifier and billing window. Windows are aligned to clock boundaries: hourly resets at the top of each hour, daily at midnight UTC, monthly on the 1st of each month, yearly on January 1st. When the count exceeds the configured limit, requests are rejected with `429 Too Many Requests`.

Supports in-memory (single instance) and Redis-backed (distributed) counting.

## Configuration

Quota is configured per route on `RouteConfig`.

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    quota:
      enabled: true
      limit: 10000
      period: daily
      key: ip
      redis: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable quota enforcement |
| `limit` | int | - | Maximum requests per period (required, > 0) |
| `period` | string | - | Billing period: `hourly`, `daily`, `monthly`, or `yearly` |
| `key` | string | - | Client identifier key (required) |
| `redis` | bool | `false` | Use Redis for distributed counting |

### Key Formats

| Key | Description |
|-----|-------------|
| `ip` | Client IP address |
| `client_id` | Authenticated client ID from JWT |
| `header:<name>` | Value of a request header |
| `jwt_claim:<name>` | Value of a JWT claim |

## Response Headers

Every request through quota-enforced routes receives quota information headers:

| Header | Description |
|--------|-------------|
| `X-Quota-Limit` | Configured quota limit |
| `X-Quota-Remaining` | Remaining requests in current window |
| `X-Quota-Reset` | Unix timestamp when the current window ends |

When quota is exceeded, the response also includes:

| Header | Description |
|--------|-------------|
| `Retry-After` | Seconds until the next window opens |

## Middleware Position

Step 5.3 in the per-route middleware chain -- after spike arrest (step 5.25) and before throttle (step 5.5). Long-term quotas are checked after short-term rate enforcement.

## Redis Mode

When `redis: true` and a Redis client is configured, quota counts are stored in Redis using `INCR` with automatic expiry. This enables distributed quota enforcement across multiple runway instances.

Redis key format: `quota:{routeID}:{period}:{windowStart}:{clientKey}`

## Admin API

```
GET /quotas
```

Returns per-route quota stats:
```json
{
  "api": {
    "limit": 10000,
    "period": "daily",
    "allowed": 8500,
    "rejected": 42,
    "redis": false
  }
}
```

## Example: API Tier Limits

Enforce daily quotas per API key:

```yaml
routes:
  - id: public-api
    path: /api/v1
    backends:
      - url: http://api-backend:8080
    quota:
      enabled: true
      limit: 1000
      period: daily
      key: header:X-API-Key
```

## Example: Monthly Usage Cap with Redis

Distributed monthly quota per authenticated user:

```yaml
redis:
  address: redis:6379

routes:
  - id: premium-api
    path: /premium
    backends:
      - url: http://premium-backend:8080
    auth:
      required: true
    quota:
      enabled: true
      limit: 100000
      period: monthly
      key: jwt_claim:sub
      redis: true
```
