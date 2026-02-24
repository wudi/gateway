---
title: "Canary Deployments"
sidebar_position: 3
---

Canary deployments progressively shift traffic from stable backends to a new (canary) version, automatically rolling back if health thresholds are breached. The gateway's canary controller manages a state machine that evaluates error rate and p99 latency at each step, advancing or rolling back based on configurable thresholds.

## How It Works

1. Define traffic split groups with a `canary` group at a low initial weight
2. Configure canary steps — each step increases the canary weight and holds for a pause duration
3. The canary controller evaluates health at a configurable interval
4. If error rate or p99 latency exceeds thresholds (absolute or comparative), the deployment is rolled back
5. If all steps complete successfully, the canary is marked as completed

Weight changes are applied to the existing `WeightedBalancer` instance — sticky sessions and health state are preserved.

## Configuration

```yaml
routes:
  - id: api
    path: /api
    path_prefix: true
    traffic_split:
      - name: stable
        weight: 95
        backends:
          - url: http://v1:8080
      - name: canary
        weight: 5
        backends:
          - url: http://v2:8080
    canary:
      enabled: true
      canary_group: canary
      auto_start: true
      steps:
        - weight: 5
          pause: 5m
        - weight: 25
          pause: 10m
        - weight: 50
          pause: 15m
        - weight: 100
      analysis:
        error_threshold: 0.05           # rollback if > 5% errors (absolute)
        latency_threshold: 500ms        # rollback if p99 > 500ms (absolute)
        max_error_rate_increase: 1.5    # rollback if canary errors > 1.5x baseline
        max_latency_increase: 2.0       # rollback if canary p99 > 2x baseline
        max_failures: 3                 # tolerate up to 2 consecutive bad evals
        min_requests: 100               # skip evaluation below this count
        interval: 30s                   # evaluation frequency
```

### Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `canary.enabled` | bool | Enable canary deployment for this route |
| `canary.canary_group` | string | Name of the canary traffic split group |
| `canary.auto_start` | bool | Start progression automatically on route load (default false) |
| `canary.steps[].weight` | int | Target weight (0-100) for this step |
| `canary.steps[].pause` | duration | Hold duration before advancing to next step |
| `canary.analysis.error_threshold` | float | Max error rate (0.0-1.0) before rollback |
| `canary.analysis.latency_threshold` | duration | Max p99 latency before rollback |
| `canary.analysis.max_error_rate_increase` | float | Max canary/baseline error rate ratio (0 = disabled) |
| `canary.analysis.max_latency_increase` | float | Max canary/baseline p99 latency ratio (0 = disabled) |
| `canary.analysis.max_failures` | int | Consecutive failing evaluations before rollback (0 = immediate) |
| `canary.analysis.min_requests` | int | Minimum requests before evaluation begins |
| `canary.analysis.interval` | duration | How often to evaluate health |

### Validation Rules

- `canary` requires `traffic_split` to be configured on the route
- `canary_group` must reference an existing traffic split group name
- At least one step is required
- Step weights must be 0-100 and monotonically non-decreasing
- `error_threshold` must be 0.0-1.0
- `interval` must be >= 0
- `max_error_rate_increase` must be >= 0
- `max_latency_increase` must be >= 0
- `max_failures` must be >= 0

## Auto-Start

When `auto_start: true` is set, the canary controller transitions from `pending` to `progressing` automatically when the route is loaded (or reloaded). This enables fully automated progressive delivery — no manual `POST /canary/{route}/start` is required.

Without `auto_start`, the deployment remains in `pending` state until started via the admin API.

## Comparative Analysis

Comparative analysis evaluates canary health relative to a baseline group rather than using absolute thresholds alone. This catches relative degradation that absolute thresholds might miss — for example, a canary error rate of 5% is acceptable if the baseline is also at 4.5%, but alarming if the baseline is at 0.1%.

**Baseline group selection:** The highest-weight non-canary traffic split group is automatically selected as the baseline. Ties are broken alphabetically for determinism.

**How it works:**
- `max_error_rate_increase: 1.5` — rollback if `canary_error_rate / baseline_error_rate > 1.5`
- `max_latency_increase: 2.0` — rollback if `canary_p99 / baseline_p99 > 2.0`
- If the baseline has zero errors or zero latency, the comparative check is skipped (division by zero guard)
- Comparative checks run after absolute threshold checks — a request can fail either check

Comparative and absolute thresholds can be used together. Set either to `0` to disable it.

## Consecutive Failure Tolerance

By default (`max_failures: 0`), a single failing evaluation triggers an immediate rollback. This can cause flapping during transient spikes. Setting `max_failures` to a value greater than 1 requires that many consecutive failing evaluations before rolling back.

- `max_failures: 3` — tolerate up to 2 consecutive bad evaluations; rollback on the 3rd
- The failure counter resets to 0 on any passing evaluation
- The failure counter resets to 0 when advancing to a new step
- The current failure count is visible in the `/canary` admin endpoint snapshot

## State Machine

```
          Start() or auto_start
pending ──────────────────────> progressing
                                 |    ^
                          Pause()|    |Resume()
                                 v    |
                                paused

progressing ──(healthy + all steps done)──> completed
progressing ──(N consecutive failures)────> rolled_back
progressing ──Promote()───────────────────> completed (100%)
progressing ──Rollback()──────────────────> rolled_back
paused ──────Rollback()───────────────────> rolled_back
```

## Weight Redistribution

When the canary weight changes, the remaining weight (100 - canary) is distributed proportionally across non-canary groups based on their original weight ratios. For example, with original weights `stable: 60, beta: 30, canary: 10` and a target canary weight of 40:

- Canary: 40
- Stable: 60 * 60 / 90 = 40
- Beta: 60 * 30 / 90 = 20

The last non-canary group absorbs any rounding remainder to ensure weights always sum to 100.

## Admin API

### List Canary Deployments

```bash
curl http://localhost:8081/canary
```

Returns per-route canary status including current state, step, weights, group metrics, baseline group, consecutive failure count, and max failures.

### Control a Canary Deployment

```bash
# Start the canary (pending -> progressing)
curl -X POST http://localhost:8081/canary/api/start

# Pause (progressing -> paused)
curl -X POST http://localhost:8081/canary/api/pause

# Resume (paused -> progressing)
curl -X POST http://localhost:8081/canary/api/resume

# Promote immediately to 100% (-> completed)
curl -X POST http://localhost:8081/canary/api/promote

# Rollback to original weights (-> rolled_back)
curl -X POST http://localhost:8081/canary/api/rollback
```

Actions return `409 Conflict` if the current state does not allow the requested transition.

## Observability

The canary controller records per-group metrics:

- **Requests**: total request count
- **Errors**: 5xx response count
- **Error Rate**: errors / requests
- **P99 Latency**: 99th percentile from a ring buffer of 1000 samples

These metrics are visible in the `/canary` admin endpoint and included in `/dashboard`.

Metrics are reset for both canary and baseline groups when advancing to the next step, so each step is evaluated on fresh data.

See [Traffic Management](traffic-management.md) for traffic splitting and sticky sessions.
See [Configuration Reference](../reference/configuration-reference.md#canary-deployments) for all fields.
