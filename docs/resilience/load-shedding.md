---
title: "Load Shedding"
sidebar_position: 6
---

Load shedding protects the gateway from resource exhaustion by monitoring system metrics (CPU, memory, goroutine count) and temporarily rejecting new requests when thresholds are exceeded. This prevents cascading failures when the gateway is under extreme load.

## Overview

Unlike rate limiting (which caps request throughput) or circuit breaking (which protects individual backends), load shedding operates at the system level. It monitors the actual resource consumption of the gateway process and sheds load before the system becomes unresponsive.

When load shedding activates, the gateway returns `503 Service Unavailable` with a `Retry-After` header, signaling clients to back off temporarily.

## Configuration

Load shedding is configured at the global level:

```yaml
load_shedding:
  enabled: true
  cpu_threshold: 90          # shed load when CPU usage exceeds 90% (0-100)
  memory_threshold: 85       # shed load when memory usage exceeds 85% (0-100)
  goroutine_limit: 0         # max goroutines before shedding (0 = unlimited)
  sample_interval: 1s        # how often to sample system metrics
  cooldown_duration: 5s      # min time to keep shedding after activation
  retry_after: 5             # Retry-After header value in seconds
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable load shedding |
| `cpu_threshold` | int | `90` | CPU usage percentage threshold (0-100). When system CPU usage exceeds this value, new requests are rejected. |
| `memory_threshold` | int | `85` | Memory usage percentage threshold (0-100). When system memory usage exceeds this value, new requests are rejected. |
| `goroutine_limit` | int | `0` | Maximum number of goroutines. When exceeded, new requests are rejected. `0` means unlimited (goroutine count is not checked). |
| `sample_interval` | duration | `1s` | How often to sample CPU, memory, and goroutine metrics. Lower values react faster but add more overhead. |
| `cooldown_duration` | duration | `5s` | Minimum duration to continue shedding after activation. Prevents rapid on/off oscillation when metrics hover near thresholds. |
| `retry_after` | int | `5` | Value of the `Retry-After` header sent with 503 responses (in seconds). |

## Pipeline Position

Load shedding runs in the global handler chain, after the RequestID middleware and before the service rate limit:

```
Recovery → RealIP → HTTPS Redirect → Allowed Hosts → RequestID → Load Shedding → Service Rate Limit → ...
```

This position ensures that:
- Every rejected request has a request ID for traceability.
- Load shedding runs before any per-route logic, protecting the gateway as a whole.
- Service rate limiting is not consumed by requests that would be shed anyway.

## Behavior

1. A background goroutine samples CPU, memory, and goroutine metrics at `sample_interval`.
2. On each incoming request, the middleware checks the most recent sample against configured thresholds.
3. If **any** threshold is exceeded, the request is immediately rejected with:
   - Status: `503 Service Unavailable`
   - Header: `Retry-After: <retry_after>`
   - Body: `{"error": "service overloaded", "retry_after": <retry_after>}`
4. Once activated, shedding continues for at least `cooldown_duration` even if metrics drop below thresholds. This prevents rapid oscillation.
5. After the cooldown expires and all metrics are below thresholds, shedding deactivates and requests flow normally.

## Admin API

### GET `/load-shedding`

Returns current load shedding status and metrics.

```bash
curl http://localhost:8081/load-shedding
```

**Response (200 OK):**
```json
{
  "enabled": true,
  "shedding": false,
  "rejected": 1250,
  "allowed": 458000,
  "cpu_percent": 72.5,
  "memory_percent": 61.3,
  "goroutine_count": 1842
}
```

| Field | Description |
|-------|-------------|
| `enabled` | Whether load shedding is configured and enabled |
| `shedding` | Whether the gateway is currently shedding load |
| `rejected` | Total number of requests rejected since startup |
| `allowed` | Total number of requests allowed since startup |
| `cpu_percent` | Most recent CPU usage sample |
| `memory_percent` | Most recent memory usage sample |
| `goroutine_count` | Most recent goroutine count |

**Response when disabled:**
```json
{
  "enabled": false
}
```

## Examples

### Conservative defaults

Suitable for most production deployments:

```yaml
load_shedding:
  enabled: true
  cpu_threshold: 90
  memory_threshold: 85
  sample_interval: 1s
  cooldown_duration: 5s
  retry_after: 5
```

### Aggressive protection with goroutine limit

For proxies handling WebSocket or SSE connections where goroutine leaks are a risk:

```yaml
load_shedding:
  enabled: true
  cpu_threshold: 85
  memory_threshold: 80
  goroutine_limit: 50000
  sample_interval: 500ms
  cooldown_duration: 10s
  retry_after: 10
```

### Memory-focused (container environments)

In containerized environments with strict memory limits, focus on memory threshold:

```yaml
load_shedding:
  enabled: true
  cpu_threshold: 100        # effectively disable CPU check
  memory_threshold: 75      # shed early to avoid OOM kill
  sample_interval: 500ms
  cooldown_duration: 5s
  retry_after: 3
```

## Relationship to Other Features

| Feature | Scope | Purpose |
|---------|-------|---------|
| **Load shedding** | Global (system-level) | Protect the gateway process from resource exhaustion |
| **Service rate limit** | Global (request throughput) | Cap total request rate across all routes |
| **Rate limiting** | Per-route / per-client | Cap request rate per client or route |
| **Circuit breaker** | Per-route / per-backend | Protect individual backends from overload |
| **Adaptive concurrency** | Per-route | Dynamically adjust concurrent requests to backends |

Load shedding is the first line of defense. It activates only under extreme system pressure and should be set with thresholds above normal operating conditions.
