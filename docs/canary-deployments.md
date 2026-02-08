# Canary Deployments

Canary deployments progressively shift traffic from stable backends to a new (canary) version, automatically rolling back if health thresholds are breached. The gateway's canary controller manages a state machine that evaluates error rate and p99 latency at each step, advancing or rolling back based on configurable thresholds.

## How It Works

1. Define traffic split groups with a `canary` group at a low initial weight
2. Configure canary steps — each step increases the canary weight and holds for a pause duration
3. The canary controller evaluates health at a configurable interval
4. If error rate or p99 latency exceeds thresholds, traffic is immediately rolled back to original weights
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
      steps:
        - weight: 5
          pause: 5m
        - weight: 25
          pause: 10m
        - weight: 50
          pause: 15m
        - weight: 100
      analysis:
        error_threshold: 0.05     # rollback if > 5% errors
        latency_threshold: 500ms  # rollback if p99 > 500ms
        min_requests: 100         # skip evaluation below this count
        interval: 30s             # evaluation frequency
```

### Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `canary.enabled` | bool | Enable canary deployment for this route |
| `canary.canary_group` | string | Name of the canary traffic split group |
| `canary.steps[].weight` | int | Target weight (0-100) for this step |
| `canary.steps[].pause` | duration | Hold duration before advancing to next step |
| `canary.analysis.error_threshold` | float | Max error rate (0.0-1.0) before rollback |
| `canary.analysis.latency_threshold` | duration | Max p99 latency before rollback |
| `canary.analysis.min_requests` | int | Minimum requests before evaluation begins |
| `canary.analysis.interval` | duration | How often to evaluate health |

### Validation Rules

- `canary` requires `traffic_split` to be configured on the route
- `canary_group` must reference an existing traffic split group name
- At least one step is required
- Step weights must be 0-100 and monotonically non-decreasing
- `error_threshold` must be 0.0-1.0
- `interval` must be >= 0

## State Machine

```
          Start()
pending ──────────> progressing
                     |    ^
              Pause()|    |Resume()
                     v    |
                    paused

progressing ──(healthy + all steps done)──> completed
progressing ──(unhealthy)──────────────────> rolled_back
progressing ──Promote()────────────────────> completed (100%)
progressing ──Rollback()───────────────────> rolled_back
paused ──────Rollback()────────────────────> rolled_back
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

Returns per-route canary status including current state, step, weights, and group metrics.

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

Metrics are reset when advancing to the next step, so each step is evaluated on fresh data.

See [Traffic Management](traffic-management.md) for traffic splitting and sticky sessions.
See [Configuration Reference](configuration-reference.md#canary-deployments) for all fields.
