---
title: "Shared Retry Budget Pools"
sidebar_position: 3
---

Retry budget pools allow multiple routes to share a single retry budget, preventing retry storms across related services. Instead of each route tracking its own retry ratio independently, routes reference a named pool that enforces a combined retry-to-request ratio across all participating routes.

## How It Works

1. Define one or more named retry budget pools at the top level of the config
2. Each pool tracks total requests and total retries across all routes that reference it
3. When a route needs to retry, the pool checks whether the combined retry ratio is within budget
4. If the pool's ratio is exceeded, additional retries are suppressed even if an individual route has low retry activity
5. The `min_retries` field ensures that the pool always allows a minimum number of retries per window, even when the ratio is exhausted

This is useful when multiple routes hit the same backend cluster. Without a shared pool, each route independently allows its own 10% retry ratio, which can compound into a much higher aggregate retry load on the backend.

## Configuration

### Top-Level Pool Definitions

```yaml
retry_budgets:
  - name: backend-cluster-a
    ratio: 0.1              # max 10% of total requests can be retries
    min_retries: 5           # always allow at least 5 retries per window
    window: 10s              # sliding window duration

  - name: backend-cluster-b
    ratio: 0.05             # stricter 5% budget for sensitive service
    min_retries: 2
    window: 30s
```

### Per-Route Reference

```yaml
routes:
  - id: users-api
    path: /api/users
    path_prefix: true
    backends:
      - url: http://cluster-a-1:8080
      - url: http://cluster-a-2:8080
    retry_policy:
      max_retries: 3
      initial_backoff: 100ms
      max_backoff: 2s
      backoff_multiplier: 2.0
      retryable_statuses: [502, 503, 504]
      budget_pool: backend-cluster-a

  - id: orders-api
    path: /api/orders
    path_prefix: true
    backends:
      - url: http://cluster-a-1:8080
      - url: http://cluster-a-2:8080
    retry_policy:
      max_retries: 2
      initial_backoff: 200ms
      max_backoff: 1s
      retryable_statuses: [502, 503]
      budget_pool: backend-cluster-a

  - id: payments-api
    path: /api/payments
    path_prefix: true
    backends:
      - url: http://cluster-b-1:8080
    retry_policy:
      max_retries: 3
      initial_backoff: 50ms
      max_backoff: 500ms
      budget_pool: backend-cluster-b
```

In this example, `users-api` and `orders-api` share the `backend-cluster-a` pool. If both routes collectively send 1000 requests within a 10s window, at most 100 of those can be retries (plus the guaranteed `min_retries: 5`). The `payments-api` route uses a separate, stricter pool.

### Pool Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | - | Unique pool name (referenced by routes) |
| `ratio` | float | - | Max retry-to-request ratio (0.0-1.0) |
| `min_retries` | int | `3` | Minimum retries always allowed per window |
| `window` | duration | `10s` | Sliding window duration for tracking |

### Route Field

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `retry_policy.budget_pool` | string | - | Name of the shared retry budget pool to use |

## Mutual Exclusivity

A route's `retry_policy` can use either an inline `budget` or a `budget_pool`, but not both. Setting both is a config validation error:

```yaml
# INVALID - budget and budget_pool are mutually exclusive
retry_policy:
  max_retries: 3
  budget:
    ratio: 0.1
  budget_pool: backend-cluster-a   # validation error
```

Additionally, `budget_pool` follows the same constraint as inline budgets: it cannot be combined with hedging (`hedging.enabled: true`).

## Validation Rules

- Each pool `name` must be unique across all `retry_budgets` entries
- `ratio` must be between 0.0 and 1.0
- `min_retries` must be >= 0
- `window` must be > 0
- `budget_pool` must reference a defined pool name (unknown pool names are rejected at config load)
- `budget_pool` and inline `budget` are mutually exclusive on the same route
- `budget_pool` and `hedging.enabled: true` are mutually exclusive on the same route

## Admin API

### GET `/retry-budget-pools`

Returns the current state of all retry budget pools, including per-pool request and retry counts within the active window.

```bash
curl http://localhost:8081/retry-budget-pools
```

**Response:**
```json
{
  "backend-cluster-a": {
    "ratio": 0.1,
    "min_retries": 5,
    "window": "10s",
    "routes": ["users-api", "orders-api"],
    "window_requests": 842,
    "window_retries": 37,
    "current_ratio": 0.044,
    "budget_exhausted": false
  },
  "backend-cluster-b": {
    "ratio": 0.05,
    "min_retries": 2,
    "window": "30s",
    "routes": ["payments-api"],
    "window_requests": 210,
    "window_retries": 11,
    "current_ratio": 0.052,
    "budget_exhausted": true
  }
}
```

## Notes

- When a pool's budget is exhausted, retries for all participating routes are suppressed. The `min_retries` guarantee applies to the pool as a whole, not per-route.
- Pool counters use a sliding window with the same implementation as inline retry budgets. Requests and retries that fall outside the window are automatically expired.
- Pool state is in-memory and not shared across gateway instances. In a multi-instance deployment, each instance maintains its own pool counters.
- If a route references a `budget_pool` but has `max_retries: 0`, the pool still counts that route's requests toward the denominator but no retries will be generated.

See [Resilience](resilience.md) for inline retry budgets and hedging.
See [Configuration Reference](../reference/configuration-reference.md) for all fields.
