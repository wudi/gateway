---
title: "Resilience"
sidebar_position: 1
---

The gateway provides retry policies, retry budgets, request hedging, circuit breakers, timeouts, and maintenance mode to protect against backend failures and cascading overload.

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

### Distributed Circuit Breaker

By default, circuit breaker state is local to each gateway instance. For multi-instance deployments, use `mode: distributed` to share state via Redis:

```yaml
redis:
  address: redis:6379

routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      max_requests: 1
      timeout: 30s
      mode: distributed
```

In distributed mode, all gateway instances share failure counts and state transitions through Redis using Lua scripts for atomicity. If Redis becomes unreachable, the circuit breaker fails open (allows requests) to avoid cascading failures.

Redis key prefix: `gw:cb:{routeID}:`

### Per-Tenant Circuit Breaker Isolation

When multi-tenancy is enabled, you can isolate circuit breakers per tenant so that one tenant's failures don't trip the breaker for others:

```yaml
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      tenant_isolation: true
```

Each tenant gets a lazily-created independent breaker. Requests without a resolved tenant use the route-level breaker. Works with both `local` and `distributed` modes.

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

## Outlier Detection

Outlier detection passively observes per-backend request outcomes (error rate, p99 latency) from real traffic and temporarily ejects backends that are statistical outliers compared to their peers — without affecting healthy backends.

Unlike the circuit breaker (which trips on route-wide consecutive failures) or health checks (which probe connectivity), outlier detection uses real production traffic metrics to identify individual misbehaving backends.

```yaml
routes:
  - id: api
    path: /api
    path_prefix: true
    backends:
      - url: http://backend-1:8080
      - url: http://backend-2:8080
      - url: http://backend-3:8080
    outlier_detection:
      enabled: true
      interval: 10s              # detection evaluation frequency
      window: 30s                # sliding window for metrics
      min_requests: 10           # minimum samples before evaluation
      error_rate_threshold: 0.5  # absolute error rate threshold (0.0-1.0)
      error_rate_multiplier: 2.0 # must exceed this * median error rate
      latency_multiplier: 3.0    # p99 must exceed this * median p99
      base_ejection_duration: 30s
      max_ejection_duration: 5m
      max_ejection_percent: 50   # never eject more than 50% of backends
```

### How It Works

1. **Observe** — Every proxied request records the backend URL, HTTP status code, and response latency into a per-backend sliding window.
2. **Evaluate** — At each `interval`, the detector collects stats snapshots for all backends with at least `min_requests` samples, computes the median error rate and median p99 latency across all backends, and identifies outliers.
3. **Eject** — A backend is ejected (marked unhealthy) if its error rate exceeds both `error_rate_threshold` and `error_rate_multiplier * median`, or if its p99 latency exceeds `latency_multiplier * median_p99`. At most `max_ejection_percent` of backends can be ejected at once.
4. **Recover** — After the ejection duration expires, the backend is marked healthy again. Repeated ejections use exponential back-off: `count * base_ejection_duration`, capped at `max_ejection_duration`.

### Comparison with Circuit Breaker and Health Checks

| Feature | Scope | Signal | Effect |
|---------|-------|--------|--------|
| Circuit Breaker | Route-wide | Consecutive 5xx failures | Blocks entire route |
| Health Checks | Per-backend | Active HTTP probes | Removes from LB |
| Outlier Detection | Per-backend | Real traffic error rate + latency | Temporarily ejects from LB |

### Webhook Events

When webhooks are enabled, outlier detection emits:
- `outlier.ejected` — backend ejected with `{backend, reason}` data
- `outlier.recovered` — backend recovered with `{backend}` data

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
| `outlier_detection.enabled` | bool | Enable per-backend outlier detection |
| `outlier_detection.interval` | duration | Detection evaluation frequency (default 10s) |
| `outlier_detection.error_rate_threshold` | float | Absolute error rate threshold (0.0-1.0) |
| `outlier_detection.max_ejection_percent` | float | Max % of backends to eject (0-100) |

## Maintenance Mode

Put routes into maintenance mode to reject traffic during planned downtime. Maintenance mode returns a configurable response and can be toggled at runtime via the admin API without config reload.

```yaml
# Global — all routes enter maintenance
maintenance:
  enabled: true
  status_code: 503
  body: '{"error":"service unavailable","message":"scheduled maintenance in progress"}'
  content_type: "application/json"
  retry_after: "3600"
  exclude_paths:
    - "/health"
    - "/ready"
  exclude_ips:
    - "10.0.0.0/8"      # internal monitoring
  headers:
    X-Maintenance: "true"
```

Per-route override:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    maintenance:
      enabled: true
      body: "<h1>API Maintenance</h1>"
      content_type: "text/html"
      retry_after: "1800"
```

### Runtime Toggle

Enable or disable maintenance mode at runtime without reloading config:

```bash
# Enable maintenance for a route
curl -X POST http://localhost:8081/maintenance/api/enable

# Disable maintenance for a route
curl -X POST http://localhost:8081/maintenance/api/disable

# Check status
curl http://localhost:8081/maintenance
```

### Key Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `maintenance.enabled` | bool | false | Activate maintenance mode |
| `maintenance.status_code` | int | 503 | HTTP status code to return |
| `maintenance.body` | string | JSON error | Response body |
| `maintenance.content_type` | string | application/json | Response Content-Type |
| `maintenance.retry_after` | string | — | Retry-After header value |
| `maintenance.exclude_paths` | []string | — | Glob patterns for bypass |
| `maintenance.exclude_ips` | []string | — | IPs/CIDRs for bypass |
| `maintenance.headers` | map | — | Extra response headers |

See [Configuration Reference](../reference/configuration-reference.md#routes) for all fields.

---

## Shared Retry Budget Pools

Named retry budgets shared across multiple routes to prevent cross-route retry storms. A budget pool tracks the ratio of retries to total requests across all routes that reference it.

```yaml
retry_budgets:
  critical_pool:
    ratio: 0.2
    min_retries: 10
    window: 10s

routes:
  - id: service-a
    retry_policy:
      max_retries: 3
      budget_pool: critical_pool
  - id: service-b
    retry_policy:
      max_retries: 2
      budget_pool: critical_pool
```

When a route references a `budget_pool`, it uses that shared budget instead of an inline `budget.ratio`. The two are mutually exclusive.

Admin endpoint: `GET /retry-budget-pools` returns utilization stats for all pools.

See [Retry Budget Pools](retry-budget-pools.md) for full documentation.
