# Rate Limiting & Traffic Shaping

The gateway provides multiple layers of traffic control: rate limiting (reject excess), throttling (queue excess), bandwidth limiting (byte-rate I/O), priority admission (QoS), and fault injection (chaos testing).

## Rate Limiting

Rate limiting enforces a maximum request rate per route. Requests exceeding the limit receive `429 Too Many Requests`.

### Local Rate Limiting

Uses a token bucket algorithm in process memory:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    rate_limit:
      enabled: true
      rate: 100        # requests per period
      period: 1m
      burst: 20        # token bucket burst
      per_ip: true     # per-IP limits (false = global per-route)
```

### Local Rate Limiting — Sliding Window

Uses a sliding window counter algorithm that interpolates between two adjacent fixed-time windows. This provides near-perfect accuracy with O(1) memory per key, preventing the boundary burst issue of token buckets (where a client can use all tokens at the end of one window and all tokens at the start of the next, effectively doubling the rate).

Choose sliding window over token bucket when strict rate enforcement is more important than burst tolerance.

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    rate_limit:
      enabled: true
      rate: 100        # requests per period
      period: 1m
      algorithm: "sliding_window"   # strict rate enforcement
      per_ip: true
```

The `algorithm` field accepts `"token_bucket"` (default) or `"sliding_window"`. When omitted, the token bucket algorithm is used.

### Distributed Rate Limiting

Uses Redis sliding window for shared state across multiple gateway instances:

```yaml
redis:
  address: "redis:6379"
  password: "${REDIS_PASS}"

routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    rate_limit:
      enabled: true
      rate: 1000
      period: 1m
      mode: "distributed"    # requires redis config
```

Distributed mode uses Lua-scripted sorted set operations for atomicity. If Redis is unreachable, the limiter fails open (allows requests).

## Throttle

Throttling queues excess requests instead of rejecting them. Requests wait in a token bucket queue until capacity is available, or are rejected with `503` if the wait exceeds `max_wait`.

Throttle is configured globally or per route. Per-route config merges with (overrides) global settings.

```yaml
# Global throttle
traffic_shaping:
  throttle:
    enabled: true
    rate: 500          # tokens per second
    burst: 100         # bucket capacity
    max_wait: 10s      # max queue wait time
    per_ip: false

# Per-route override
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    traffic_shaping:
      throttle:
        enabled: true
        rate: 50
        burst: 10
        max_wait: 5s
```

Throttle runs after rate limiting — rejected requests never enter the throttle queue.

## Bandwidth Limiting

Limits the byte rate of request bodies (uploads) and response bodies (downloads) per route:

```yaml
routes:
  - id: "upload"
    path: "/upload"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    traffic_shaping:
      bandwidth:
        enabled: true
        request_rate: 1048576    # 1 MB/s upload limit
        response_rate: 5242880   # 5 MB/s download limit
        request_burst: 1048576   # burst size (defaults to rate)
        response_burst: 5242880
```

Bandwidth limiting wraps the request body reader and response writer with rate-limited I/O.

## Priority Admission

Priority admission provides QoS by limiting concurrent requests through a shared semaphore with a priority queue. Higher-priority requests are admitted first.

Global configuration defines the semaphore capacity. Per-route configuration defines how requests are classified into priority levels.

```yaml
# Global: enable and set capacity
traffic_shaping:
  priority:
    enabled: true
    max_concurrent: 100    # total concurrent requests
    max_wait: 30s          # max queue wait time
    default_level: 5       # 1=highest, 10=lowest

# Per-route: classify requests
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    traffic_shaping:
      priority:
        enabled: true
        levels:
          - level: 1
            headers:
              X-Priority: "critical"
          - level: 2
            client_ids: ["premium-client"]
          - level: 8
            headers:
              X-Priority: "background"
```

Priority runs after authentication, so `client_ids` matching uses the authenticated client identity.

## Fault Injection

Inject artificial delays and/or HTTP error responses for chaos testing. Configured per route:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    traffic_shaping:
      fault_injection:
        enabled: true
        delay:
          percentage: 10       # 10% of requests are delayed
          duration: 500ms
        abort:
          percentage: 5        # 5% of requests return error
          status_code: 503
```

Abort is evaluated first — if a request is aborted, the delay is skipped. Both use independent random rolls, so a request could theoretically match both (abort takes precedence).

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `rate_limit.mode` | string | `local` (default) or `distributed` |
| `rate_limit.algorithm` | string | `token_bucket` (default) or `sliding_window` |
| `rate_limit.per_ip` | bool | Per-IP or per-route limiting |
| `traffic_shaping.throttle.rate` | int | Tokens per second |
| `traffic_shaping.throttle.max_wait` | duration | Max queue time before 503 |
| `traffic_shaping.bandwidth.request_rate` | int64 | Upload bytes/sec (0 = unlimited) |
| `traffic_shaping.bandwidth.response_rate` | int64 | Download bytes/sec (0 = unlimited) |
| `traffic_shaping.priority.max_concurrent` | int | Shared semaphore capacity |
| `traffic_shaping.fault_injection.delay.percentage` | int | % of requests to delay (0-100) |
| `traffic_shaping.fault_injection.abort.status_code` | int | HTTP status for aborted requests |

See [Configuration Reference](configuration-reference.md#traffic-shaping-global) for all fields.
