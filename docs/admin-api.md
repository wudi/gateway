# Admin API Reference

The admin API runs on a separate port (default 8081) and exposes health checks, feature status, metrics, and configuration management endpoints.

```yaml
admin:
  enabled: true
  port: 8081
  metrics:
    enabled: true
    path: "/metrics"
  readiness:
    min_healthy_backends: 1
    require_redis: false
```

## Health & Readiness

### GET `/health` (alias: `/healthz`)

Returns gateway health status with dependency checks.

```bash
curl http://localhost:8081/health
```

**Response (200 OK):**
```json
{
  "status": "ok",
  "timestamp": "2026-01-15T10:30:00Z",
  "uptime": "24h15m30s",
  "checks": {
    "backends": {
      "status": "ok",
      "total_routes": 5,
      "healthy_routes": 5
    },
    "redis": {
      "status": "ok"
    },
    "tracing": {
      "status": "ok"
    }
  }
}
```

Returns `503` with `"status": "degraded"` when any check fails (e.g., all backends unhealthy). The `redis` check is only included when Redis is configured, and `tracing` only when tracing is enabled.

### GET `/ready` (alias: `/readyz`)

Returns readiness status based on configured thresholds.

```bash
curl http://localhost:8081/ready
```

**Response (200 OK):**
```json
{
  "status": "ready",
  "routes": 5,
  "healthy_routes": 5,
  "listeners": 2
}
```

Returns `503` with a `reasons` array when not ready:
```json
{
  "status": "not_ready",
  "routes": 5,
  "healthy_routes": 0,
  "listeners": 2,
  "reasons": ["need 1 healthy routes, have 0"]
}
```

Readiness fails when healthy routes are below `min_healthy_backends` (default 1), or when `require_redis: true` and Redis is unreachable.

## Feature Status Endpoints

All feature endpoints return JSON with per-route status and metrics.

| Endpoint | Description |
|----------|-------------|
| `GET /stats` | Overall gateway statistics (route/backend/listener counts) |
| `GET /listeners` | Active listeners with protocol and address |
| `GET /routes` | All routes with matchers (path, methods, domains, headers, query) |
| `GET /registry` | Configured registry type |
| `GET /backends` | Backend health status with latency, last check time, and health check config |
| `GET /circuit-breakers` | Circuit breaker state per route (closed/open/half-open) |
| `GET /cache` | Cache statistics (hits, misses, size, evictions). For distributed mode, size is Redis key count; hits/misses are local per-instance counters. |
| `GET /retries` | Retry metrics per route (attempts, budget exhaustion, hedged requests) |
| `GET /rules` | Rules engine status (global + per-route rules and metrics) |
| `GET /protocol-translators` | gRPC translator statistics |
| `GET /traffic-shaping` | Throttle, bandwidth, priority, fault injection, and adaptive concurrency stats |
| `GET /adaptive-concurrency` | Adaptive concurrency limiter stats (limit, in-flight, EWMA, rejections) |
| `GET /mirrors` | Mirror metrics (counts, latencies, comparisons) |
| `GET /traffic-splits` | Traffic split distribution per route |
| `GET /rate-limits` | Rate limiter mode and algorithm per route |
| `GET /tracing` | Tracing/OTEL status |
| `GET /waf` | WAF statistics (blocks, detections) |
| `GET /graphql` | GraphQL parser statistics (depth/complexity checks) |
| `GET /coalesce` | Request coalescing stats (groups, coalesced requests, timeouts) |
| `GET /load-balancers` | Load balancer info (algorithm, backend states) |
| `GET /canary` | Canary deployment status per route |
| `POST /canary/{route}/{action}` | Control canary (start, pause, resume, promote, rollback) |
| `GET /ext-auth` | External auth metrics (total, allowed, denied, errors, cache hits, latencies) |
| `GET /versioning` | API versioning stats per route (source, default version, per-version request counts, deprecation info) |
| `GET /access-log` | Per-route access log config status (enabled, format, body capture, conditions) |
| `GET /openapi` | OpenAPI validation stats per route (spec, operation, request/response validation, metrics) |
| `GET /timeouts` | Per-route timeout policy config and metrics (request/backend/idle/header timeouts, timeout counts) |
| `GET /upstreams` | Named upstream pool definitions (backends, LB algorithm, health check config) |
| `GET /error-pages` | Custom error page configuration per route (configured pages, render metrics) |

### Example: Querying Feature Endpoints

```bash
# Check overall stats
curl http://localhost:8081/stats

# Check backend health
curl http://localhost:8081/backends

# Check circuit breaker states
curl http://localhost:8081/circuit-breakers

# Check cache hit rates
curl http://localhost:8081/cache

# Check rate limiter modes and algorithms
curl http://localhost:8081/rate-limits
```

## Dashboard

### GET `/dashboard`

Aggregated view of all feature statistics in a single response. Includes uptime, circuit breakers, cache, retries, traffic splits, upstreams, WAF, tracing, and TCP/UDP route stats.

```bash
curl http://localhost:8081/dashboard
```

## Prometheus Metrics

### GET `/metrics`

Prometheus scrape endpoint (only available when `admin.metrics.enabled: true`). The path is configurable via `admin.metrics.path`.

```bash
curl http://localhost:8081/metrics
```

## Configuration Reload

### POST `/reload`

Trigger a hot configuration reload from disk. Equivalent to sending `SIGHUP`.

```bash
curl -X POST http://localhost:8081/reload
```

**Response:**
```json
{
  "Success": true,
  "Timestamp": "2026-01-15T10:30:00Z",
  "Changes": ["route:api-v2 added", "route:old-api removed"]
}
```

On failure:
```json
{
  "Success": false,
  "Timestamp": "2026-01-15T10:30:00Z",
  "Error": "validation error: route 'bad' missing backends"
}
```

Returns `405 Method Not Allowed` for non-POST requests.

### GET `/reload/status`

Returns the last 50 reload results (most recent first).

```bash
curl http://localhost:8081/reload/status
```

## API Key Management

### `/admin/keys`

Only available when API key authentication is configured. Allows runtime management of API keys without config reload.

### List Keys

```bash
curl http://localhost:8081/admin/keys
```

Returns a map of masked keys to their metadata. Keys are partially masked (first 4 and last 4 characters visible):

```json
{
  "abc1****xyz9": {
    "client_id": "client-1",
    "name": "Production Client",
    "roles": ["admin"]
  }
}
```

### Create Key

```bash
curl -X POST http://localhost:8081/admin/keys \
  -H "Content-Type: application/json" \
  -d '{
    "key": "new-secret-key-value",
    "client_id": "client-3",
    "name": "New Client",
    "expires_at": "2027-01-01T00:00:00Z",
    "roles": ["read"]
  }'
```

**Response (201 Created):**
```json
{"status": "created"}
```

The `key` and `client_id` fields are required. `name`, `expires_at` (RFC3339), and `roles` are optional.

### Revoke Key

```bash
curl -X DELETE http://localhost:8081/admin/keys \
  -H "Content-Type: application/json" \
  -d '{"key": "new-secret-key-value"}'
```

**Response:**
```json
{"status": "deleted"}
```

## Error Pages

### GET `/error-pages`

Returns custom error page configuration and render metrics per route.

```bash
curl http://localhost:8081/error-pages
```

**Response:**
```json
{
  "my-api": {
    "pages": ["404", "5xx", "default"],
    "metrics": {
      "total_rendered": 15
    }
  }
}
```

## Nonces (Replay Prevention)

### GET `/nonces`

Returns nonce checker configuration and metrics per route.

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

## CSRF Protection

### GET `/csrf`

Returns per-route CSRF protection status and metrics.

```bash
curl http://localhost:8081/csrf
```

**Response:**
```json
{
  "web-app": {
    "cookie_name": "_csrf",
    "header_name": "X-CSRF-Token",
    "token_ttl": "1h0m0s",
    "shadow_mode": false,
    "inject_token": true,
    "total_requests": 5000,
    "token_generated": 2000,
    "validation_success": 2900,
    "validation_failed": 100,
    "origin_check_failed": 5,
    "missing_token": 80,
    "expired_token": 10,
    "invalid_signature": 5
  }
}
```

## Outlier Detection

### GET `/outlier-detection`

Returns per-route outlier detection status including per-backend stats, ejected backends, and aggregate counters.

```bash
curl http://localhost:8081/outlier-detection
```

**Response:**
```json
{
  "api": {
    "route_id": "api",
    "backend_stats": {
      "http://backend-1:8080": {
        "total_requests": 150,
        "total_errors": 2,
        "error_rate": 0.013,
        "p50": "5ms",
        "p99": "45ms"
      },
      "http://backend-2:8080": {
        "total_requests": 148,
        "total_errors": 75,
        "error_rate": 0.507,
        "p50": "120ms",
        "p99": "500ms"
      }
    },
    "ejected_backends": {
      "http://backend-2:8080": {
        "ejected_at": "2025-01-15T10:30:00Z",
        "duration": "30s",
        "count": 1,
        "reason": "error_rate"
      }
    },
    "total_ejections": 3,
    "total_recoveries": 2
  }
}
```

## Geo Filtering

### GET `/geo`

Returns per-route geo filtering status including configured allow/deny lists and metrics.

```bash
curl http://localhost:8081/geo
```

**Response:**
```json
{
  "api": {
    "route_id": "api",
    "enabled": true,
    "allow_countries": ["US", "CA"],
    "deny_countries": [],
    "order": "deny_first",
    "shadow_mode": false,
    "inject_headers": true,
    "metrics": {
      "total_requests": 500,
      "allowed": 480,
      "denied": 15,
      "lookup_errors": 5
    }
  }
}
```

## Webhooks

### GET `/webhooks`

Returns webhook dispatcher status, queue usage, delivery metrics, and recent events. Returns `{"enabled": false}` when webhooks are not configured.

```bash
curl http://localhost:8081/webhooks
```

**Response:**
```json
{
  "enabled": true,
  "endpoints": 2,
  "queue_size": 1000,
  "queue_used": 0,
  "metrics": {
    "total_emitted": 42,
    "total_delivered": 40,
    "total_failed": 1,
    "total_dropped": 0,
    "total_retries": 3
  },
  "recent_events": []
}
```

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `admin.enabled` | bool | Enable admin API |
| `admin.port` | int | Admin API port (default 8081) |
| `admin.metrics.enabled` | bool | Enable Prometheus metrics |
| `admin.metrics.path` | string | Metrics endpoint path (default `/metrics`) |
| `admin.readiness.min_healthy_backends` | int | Min healthy backends for ready (default 1) |
| `admin.readiness.require_redis` | bool | Require Redis for ready |

See [Configuration Reference](configuration-reference.md#admin) for all fields.
