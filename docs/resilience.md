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

Set per-route request and idle timeouts:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    timeout: 30s              # simple request timeout
    # Or use timeout_policy for more control:
    timeout_policy:
      request: 30s            # total request timeout
      idle: 60s               # idle connection timeout
```

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

See [Configuration Reference](configuration-reference.md#routes) for all fields.
