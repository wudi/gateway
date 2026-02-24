---
title: "Request Cost Tracking"
sidebar_position: 6
---

The request cost middleware assigns a cost to each request and optionally enforces per-consumer cost budgets. This is useful for metering API usage beyond simple request counts, where different operations have different resource costs.

## Configuration

Per-route:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    request_cost:
      enabled: true
      cost: 1
      cost_by_method:
        POST: 5
        PUT: 3
        DELETE: 2
      key: "client_id"
      budget:
        limit: 10000
        window: "hour"
        action: "reject"
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `request_cost.enabled` | bool | false | Enable cost tracking |
| `request_cost.cost` | int | 1 | Default cost per request |
| `request_cost.cost_by_method` | map[string]int | -- | Per-HTTP-method cost overrides |
| `request_cost.key` | string | -- | Consumer key: `ip`, `client_id`, `header:<name>` |
| `request_cost.budget` | object | -- | Optional per-consumer budget enforcement |

### Budget Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `budget.limit` | int64 | -- | Maximum cost per window |
| `budget.window` | string | -- | `hour`, `day`, `month`, or Go duration |
| `budget.action` | string | reject | `reject` (return 429) or `log_only` |

## Behavior

1. Each request is assigned a cost -- either the `cost_by_method` value for the request method or the default `cost`.
2. The `X-Request-Cost` response header is set with the request's cost value.
3. If a `budget` is configured, the middleware tracks cumulative cost per consumer (identified by `key`) within the sliding window.
4. When a consumer exceeds the budget limit and `action` is `reject`, the request is rejected with `429 Too Many Requests`.

## Admin Endpoint

`GET /request-cost` returns per-route cost tracking statistics.

```bash
curl http://localhost:8081/request-cost
```

See [Configuration Reference](../reference/configuration-reference.md#request-cost-per-route) for field details.
