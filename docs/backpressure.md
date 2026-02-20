# Backend Backpressure

Backend backpressure detection allows the gateway to automatically detect when backends are overloaded and temporarily stop sending them traffic. When a backend responds with an overload status code (e.g., `429 Too Many Requests` or `503 Service Unavailable`), the gateway parses the `Retry-After` header and marks the backend as temporarily unhealthy, giving it time to recover.

## Overview

Without backpressure handling, the gateway continues routing traffic to overloaded backends, which prolongs the overload condition and can lead to cascading failures. Backend backpressure closes this loop by:

1. Detecting overload via configurable HTTP status codes
2. Parsing `Retry-After` headers from backend responses to determine the backoff duration
3. Temporarily marking the backend as unhealthy in the load balancer
4. Automatically restoring the backend after the delay expires

This is complementary to circuit breaking and adaptive concurrency. While circuit breakers react to error rates over time and adaptive concurrency adjusts based on latency, backpressure reacts to explicit signals from the backend itself.

## Configuration

Backpressure is configured per-route:

```yaml
routes:
  - id: "my-api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend-1:9000"
      - url: "http://backend-2:9000"
    backpressure:
      enabled: true
      status_codes:
        - 429
        - 503
      max_retry_after: 60s
      default_delay: 5s
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable backend backpressure detection |
| `status_codes` | []int | `[429, 503]` | HTTP status codes that indicate backend overload |
| `max_retry_after` | duration | `60s` | Maximum backoff duration. If the backend's `Retry-After` header exceeds this value, it is clamped to `max_retry_after`. |
| `default_delay` | duration | `5s` | Backoff duration when the backend does not include a `Retry-After` header |

## How It Works

1. The gateway proxies a request to a backend.
2. If the backend responds with a status code in `status_codes` (e.g., `429` or `503`):
   - The gateway checks for a `Retry-After` header on the response.
   - If `Retry-After` is present and contains a number of seconds (e.g., `Retry-After: 30`), the backend is marked unhealthy for that duration, clamped to `max_retry_after`.
   - If `Retry-After` is present as an HTTP-date, it is converted to a duration from now, clamped to `max_retry_after`.
   - If `Retry-After` is absent, the backend is marked unhealthy for `default_delay`.
3. While the backend is marked unhealthy, the load balancer skips it when selecting backends for new requests.
4. After the delay expires, the backend is automatically restored to healthy status and begins receiving traffic again.
5. The original overload response (429/503) is still returned to the client that triggered the detection.

## Pipeline Position

Backpressure runs at step 12.55 in the per-route middleware chain, after adaptive concurrency and before proxy rate limiting:

```
... → circuitBreakerMW (12) → outlierDetectionMW (12.25) →
adaptiveConcurrencyMW (12.5) → backpressureMW (12.55) →
proxyRateLimitMW (12.6) → compressionMW (13) → ...
```

This position ensures that:
- Circuit breaker state is evaluated before backpressure (an open circuit prevents requests from reaching backpressure)
- Adaptive concurrency limits are checked first (requests within the concurrency limit proceed to backpressure)
- Backpressure can observe the actual backend response and make marking decisions

## Admin API

### GET `/backpressure`

Returns per-route backpressure status, including currently backed-off backends.

```bash
curl http://localhost:8081/backpressure
```

**Response (200 OK):**
```json
{
  "my-api": {
    "enabled": true,
    "status_codes": [429, 503],
    "max_retry_after": "1m0s",
    "default_delay": "5s",
    "backed_off_backends": {
      "http://backend-2:9000": {
        "until": "2026-02-20T10:35:30Z",
        "remaining": "25s",
        "reason": 429
      }
    },
    "total_backoffs": 12,
    "active_backoffs": 1
  }
}
```

| Field | Description |
|-------|-------------|
| `enabled` | Whether backpressure is enabled for this route |
| `status_codes` | Configured overload status codes |
| `max_retry_after` | Maximum allowed backoff duration |
| `default_delay` | Default backoff when no Retry-After header |
| `backed_off_backends` | Map of backends currently in backoff, with expiry time, remaining duration, and triggering status code |
| `total_backoffs` | Total number of backoff events since startup |
| `active_backoffs` | Number of backends currently in backoff |

## Examples

### Basic overload protection

Detect 429 and 503 responses with default settings:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://api-1:8080"
      - url: "http://api-2:8080"
      - url: "http://api-3:8080"
    backpressure:
      enabled: true
```

### Strict backoff with short maximum

For APIs where backend overload should recover quickly:

```yaml
routes:
  - id: "realtime"
    path: "/realtime"
    path_prefix: true
    backends:
      - url: "http://rt-1:8080"
      - url: "http://rt-2:8080"
    backpressure:
      enabled: true
      status_codes: [429, 503]
      max_retry_after: 15s
      default_delay: 2s
```

### Combined with circuit breaker

Backpressure and circuit breaker work together. The circuit breaker opens after sustained errors, while backpressure reacts to individual overload signals:

```yaml
routes:
  - id: "protected-api"
    path: "/api/v2"
    path_prefix: true
    backends:
      - url: "http://v2-1:8080"
      - url: "http://v2-2:8080"
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      timeout: 30s
    backpressure:
      enabled: true
      status_codes: [429, 503]
      max_retry_after: 60s
      default_delay: 10s
```

In this setup:
- A single `429` response triggers backpressure on that specific backend (removes it temporarily)
- If errors accumulate across all backends, the circuit breaker opens (blocks all traffic to the route)

## Relationship to Other Features

| Feature | Trigger | Scope | Recovery |
|---------|---------|-------|----------|
| **Backpressure** | Explicit backend signal (status code) | Per-backend | Timer based on Retry-After |
| **Circuit breaker** | Error rate over time | Per-route (all backends) | Half-open probe after timeout |
| **Outlier detection** | Statistical anomaly (error rate, latency) | Per-backend | Ejection timer with backoff |
| **Adaptive concurrency** | Latency degradation | Per-route | Continuous AIMD adjustment |
