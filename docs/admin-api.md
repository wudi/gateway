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
| `GET /backends` | Backend health status with latency and last check time |
| `GET /circuit-breakers` | Circuit breaker state per route (closed/open/half-open) |
| `GET /cache` | Cache statistics (hits, misses, size, evictions) |
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

Aggregated view of all feature statistics in a single response. Includes uptime, circuit breakers, cache, retries, traffic splits, WAF, tracing, and TCP/UDP route stats.

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
