---
title: "A/B Testing"
sidebar_position: 5
---

A/B testing enables metric collection for traffic split groups, allowing you to compare variant performance. It builds on the existing `traffic_split` and `WeightedBalancer` infrastructure.

## Relationship to Traffic Split

A/B testing requires `traffic_split` to be configured on the route. The `traffic_split` defines the groups and their weights; `ab_test` adds per-group metric collection (request count, error rate, p99 latency) on top.

## Mutual Exclusivity

A/B testing is mutually exclusive with:
- **Canary deployments** — canary uses automated progressive rollout with rollback
- **Blue-green deployments** — blue-green uses active/inactive group switching

Only one of these three traffic management strategies can be active per route.

## Configuration

```yaml
routes:
  - id: homepage
    path: /
    traffic_split:
      - name: control
        weight: 50
        backends:
          - url: http://control:8080
      - name: experiment
        weight: 50
        backends:
          - url: http://experiment:8080
    ab_test:
      enabled: true
      experiment_name: homepage-redesign
```

### Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `enabled` | bool | yes | Enable A/B test metric collection |
| `experiment_name` | string | yes | Human-readable name for the experiment |

### With Sticky Sessions

To ensure users consistently see the same variant, combine with sticky sessions:

```yaml
routes:
  - id: homepage
    path: /
    traffic_split:
      - name: control
        weight: 50
        backends:
          - url: http://control:8080
      - name: experiment
        weight: 50
        backends:
          - url: http://experiment:8080
    sticky:
      enabled: true
      mode: cookie
      cookie_name: X-AB-Group
      ttl: 24h
    ab_test:
      enabled: true
      experiment_name: homepage-redesign
```

## Metrics

The admin API exposes A/B test metrics at `GET /ab-tests`:

```json
{
  "homepage": {
    "route_id": "homepage",
    "experiment_name": "homepage-redesign",
    "started_at": "2026-02-21T10:00:00Z",
    "duration_sec": 3600,
    "groups": {
      "control": {
        "requests": 5000,
        "errors": 25,
        "error_rate": 0.005,
        "latency_p99_ms": 120.5
      },
      "experiment": {
        "requests": 5100,
        "errors": 10,
        "error_rate": 0.00196,
        "latency_p99_ms": 95.2
      }
    }
  }
}
```

Per-group metrics:

| Metric | Description |
|--------|-------------|
| `requests` | Total request count |
| `errors` | Count of 5xx responses |
| `error_rate` | Error rate (0.0-1.0) |
| `latency_p99_ms` | 99th percentile latency in milliseconds |

## Admin Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/ab-tests` | Returns metrics for all A/B tests |
| POST | `/ab-tests/{route}/reset` | Resets accumulated metrics and restarts the timer |

### Reset Example

```bash
curl -X POST http://admin:9090/ab-tests/homepage/reset
```

Response:
```json
{"status": "ok", "action": "reset", "route": "homepage"}
```
