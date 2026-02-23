# SLI/SLO Enforcement

The gateway can track Service Level Indicators (SLIs) per route and enforce Service Level Objectives (SLOs) by monitoring error budgets in a sliding window. When the error budget is exhausted, configurable actions are triggered: logging warnings, adding budget headers, or shedding load.

## Configuration

```yaml
routes:
  - id: "critical-api"
    path: "/api/critical"
    backends:
      - url: "http://backend:8080"
    slo:
      enabled: true
      target: 0.999              # 99.9% availability target
      window: 720h               # 30-day sliding window
      actions:
        - log_warning            # log when budget exhausted
        - add_header             # add X-SLO-Budget-Remaining header
        - shed_load              # probabilistically reject requests
      shed_load_percent: 10      # reject 10% of requests when budget exhausted
      error_codes:               # which status codes count as errors
        - 500
        - 502
        - 503
        - 504
```

## Error Budget

The error budget is calculated as:

```
allowed_error_rate = 1.0 - target
actual_error_rate = errors / total_requests
budget_remaining = 1.0 - (actual_error_rate / allowed_error_rate)
```

For example, with `target: 0.999`:
- Allowed error rate: 0.1%
- If actual error rate is 0.05%, budget remaining = 50%
- If actual error rate is 0.1%, budget remaining = 0% (exhausted)
- If actual error rate is 0.2%, budget remaining = -100% (over budget)

## Actions

### `log_warning`

When the error budget is exhausted (budget_remaining <= 0), a warning is logged on each request with the path, target, and response status code.

### `add_header`

Adds an `X-SLO-Budget-Remaining` header to every response with the current budget as a decimal (e.g., `0.5000` for 50% remaining). Consumers can use this to proactively adjust behavior.

### `shed_load`

When the budget is exhausted, probabilistically rejects requests with HTTP 503 and a `Retry-After: 5` header. The rejection probability is controlled by `shed_load_percent` (default 10%). This prevents cascading failures by reducing load on struggling backends.

## Sliding Window

Metrics are tracked in a 60-bucket ring buffer. The window duration is divided into 60 equal buckets, and expired buckets are automatically zeroed. This provides smooth metric aggregation without large step changes.

## Default Error Codes

If `error_codes` is not specified, status codes 500-599 are counted as errors. You can customize this to include other codes (e.g., 429 for rate limiting) or exclude specific 5xx codes.

## Middleware Position

The SLO middleware runs at step **1.1** in the middleware chain:
- After metrics (1) — observes true final status codes
- Before all other middleware — load shedding avoids wasting work on expensive downstream middleware

## Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `slo.enabled` | bool | Enable SLO tracking |
| `slo.target` | float64 | Availability target, e.g. 0.999 (must be in (0, 1) exclusive) |
| `slo.window` | duration | Sliding window duration (must be >= 1 minute) |
| `slo.actions` | []string | Actions: "log_warning", "add_header", "shed_load" |
| `slo.shed_load_percent` | float64 | Rejection percentage when budget exhausted (0-100, default 10) |
| `slo.error_codes` | []int | HTTP status codes that count as errors (default 500-599) |

## Admin API

- **GET** `/slo` — Returns per-route SLO stats including target, total requests, errors, error rate, budget remaining, and shed count.
