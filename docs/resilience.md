# Resilience

The gateway provides retry policies, retry budgets, request hedging, circuit breakers, and timeouts to protect against backend failures and cascading overload.

## Retry Policy

Configure exponential backoff retries with control over which responses and methods are retryable:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    retry_policy:
      max_retries: 3
      initial_backoff: 100ms
      max_backoff: 2s
      backoff_multiplier: 2.0
      retryable_statuses: [502, 503, 504]
      retryable_methods: ["GET", "PUT", "DELETE"]
      per_try_timeout: 5s
```

Retries use exponential backoff: each attempt waits `initial_backoff * backoff_multiplier^attempt`, capped at `max_backoff`.

## Retry Budget

A retry budget prevents retry storms by limiting the ratio of retries to total requests over a sliding time window:

```yaml
retry_policy:
  max_retries: 3
  initial_backoff: 100ms
  budget:
    ratio: 0.1          # max 10% of requests can be retries
    min_retries: 3       # always allow at least 3 retries per window
    window: 10s          # sliding window duration
```

When the budget is exhausted, additional retries are suppressed. The `min_retries` field ensures that low-traffic routes can still retry.

## Request Hedging

Hedging sends speculative duplicate requests to different backends after a delay. The first successful response wins; all other in-flight requests are cancelled.

```yaml
retry_policy:
  hedging:
    enabled: true
    max_requests: 3      # original + 2 hedged (minimum 2)
    delay: 100ms         # wait before sending hedged request
```

**Hedging and retries are mutually exclusive.** You cannot set `max_retries > 0` and `hedging.enabled: true` on the same route — this is a config validation error. Hedging is best for latency-sensitive idempotent requests (reads, lookups).

## Circuit Breaker

The circuit breaker stops sending traffic to a failing backend, giving it time to recover:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    circuit_breaker:
      enabled: true
      failure_threshold: 5    # consecutive failures to open
      max_requests: 1         # requests allowed in half-open state
      timeout: 30s            # wait before transitioning to half-open
```

States:
- **Closed** — normal operation, failures are counted
- **Open** — all requests rejected with 503, entered after `failure_threshold` consecutive failures
- **Half-open** — entered after `timeout`, allows `max_requests` through to test recovery

The circuit breaker operates after the cache check — a cache hit never touches the circuit breaker.

## Timeouts

Set per-route timeout policies with four levels of control:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    timeout: 30s              # simple request timeout (legacy)
    # Or use timeout_policy for comprehensive control:
    timeout_policy:
      request: 30s            # total end-to-end request timeout
      backend: 5s             # per-backend-call timeout (each attempt)
      header_timeout: 3s      # timeout waiting for response headers
      idle: 60s               # idle timeout for response body streaming
    retry_policy:
      max_retries: 3          # each attempt gets 5s (backend), total capped at 30s
```

### Timeout Fields

- **`request`** — Total end-to-end timeout for the entire request lifecycle. The timeout middleware sets a context deadline before the request enters the middleware chain. Any 504 response includes a `Retry-After` header.
- **`backend`** — Per-backend-call timeout. When retries are configured, each attempt is individually capped at this duration. When `retry_policy.per_try_timeout` is also set, `backend` takes precedence.
- **`header_timeout`** — Maximum time to wait for response headers from the backend. This is enforced as part of the backend timeout via `http.Transport` semantics.
- **`idle`** — Idle timeout for response body streaming. If no data is received from the backend for this duration during body transfer, the connection is terminated with `context.DeadlineExceeded`.

### Interactions with Retries

When both `timeout_policy.backend` and `retry_policy` are configured, each retry attempt is individually subject to the backend timeout, while the overall request timeout caps the total time across all attempts. For example, with `request: 30s`, `backend: 5s`, and `max_retries: 3`, the gateway allows up to 4 attempts (1 original + 3 retries) of 5s each, all within the 30s request deadline.

### Validation

- All timeout durations must be >= 0
- `backend` must be <= `request` when both are set
- `header_timeout` must be <= `backend` (or `request` if no backend) when both are set

## Health Checks

Backend health checks run in the background to detect unhealthy backends and remove them from load balancing. By default, checks send `GET /health` every 10s and consider 200-399 as healthy. Configure globally or per-backend:

### Global Settings

```yaml
health_check:
  path: "/status"
  method: "HEAD"
  interval: 15s
  timeout: 5s
  healthy_after: 3
  unhealthy_after: 2
  expected_status: ["2xx"]
```

### Per-Backend Override

Per-backend settings override the global config. Unset fields inherit from the global config:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend-a:9000"
        health_check:
          path: "/healthz"
          expected_status: ["200"]
      - url: "http://backend-b:9000"
        health_check:
          method: "POST"
          path: "/health"
          timeout: 2s
```

### Health Check Fields

- **`path`** — URL path appended to the backend URL (default `/health`)
- **`method`** — HTTP method: GET, HEAD, OPTIONS, or POST (default `GET`)
- **`interval`** — Time between checks (default `10s`)
- **`timeout`** — Per-check timeout, must be <= interval (default `5s`)
- **`healthy_after`** — Consecutive successes needed to mark backend healthy (default `2`)
- **`unhealthy_after`** — Consecutive failures needed to mark backend unhealthy (default `3`)
- **`expected_status`** — Status codes/ranges considered healthy (default `200-399`). Accepts patterns: `"200"` (exact), `"2xx"` (class), `"200-299"` (range)

### Validation

- `method` must be GET, HEAD, OPTIONS, or POST
- `interval` and `timeout` must be >= 0
- `timeout` must be <= `interval` when both are set
- `healthy_after` and `unhealthy_after` must be >= 0
- `expected_status` entries must be valid status patterns

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `retry_policy.max_retries` | int | Maximum retry attempts |
| `retry_policy.backoff_multiplier` | float | Multiplier per attempt (must be >= 1.0) |
| `retry_policy.budget.ratio` | float | Max retry ratio (0.0-1.0) |
| `retry_policy.hedging.enabled` | bool | Enable speculative hedging |
| `retry_policy.hedging.delay` | duration | Wait before hedged request |
| `circuit_breaker.failure_threshold` | int | Failures before circuit opens |
| `circuit_breaker.timeout` | duration | Time in open state before half-open |
| `timeout_policy.request` | duration | Total end-to-end request timeout |
| `timeout_policy.backend` | duration | Per-backend-call timeout |
| `timeout_policy.header_timeout` | duration | Response header timeout |
| `timeout_policy.idle` | duration | Streaming idle timeout |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
