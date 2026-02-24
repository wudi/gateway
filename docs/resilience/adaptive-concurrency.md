---
title: "Adaptive Concurrency Limiting"
sidebar_position: 4
---

Adaptive concurrency limiting automatically discovers the optimal number of concurrent requests each route can handle, shedding load before failures cascade. Unlike static concurrency limits, the adaptive limiter continuously adjusts based on observed latency using a TCP Vegas-inspired AIMD (Additive Increase, Multiplicative Decrease) algorithm.

## How It Works

1. The limiter tracks per-route latency via EWMA (Exponentially Weighted Moving Average)
2. A slowly-decaying `minLatency` baseline estimates the no-queuing latency
3. Every `adjustment_interval`, the limiter computes `gradient = ewmaLatency / minLatency`
4. If `gradient < latency_tolerance`: limit increases by 1 (additive increase)
5. If `gradient >= latency_tolerance`: limit decreases proportionally (multiplicative decrease: `limit * minLatency / ewmaLatency`)
6. The limit is clamped to `[min_concurrency, max_concurrency]`
7. Requests exceeding the limit are immediately rejected with 503 (no queuing)

Only successful responses (2xx/3xx) contribute to the latency EWMA. Error responses (5xx) often return fast and would skew the baseline downward, causing the limiter to over-admit.

The limiter starts at `max_concurrency` (wide open) and narrows as data arrives.

## Configuration

### Global (applies to all routes as default)

```yaml
traffic_shaping:
  adaptive_concurrency:
    enabled: true
    min_concurrency: 10
    max_concurrency: 200
    latency_tolerance: 2.0
    adjustment_interval: 5s
    smoothing_factor: 0.5
    min_latency_samples: 25
```

### Per-route (overrides global)

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    traffic_shaping:
      adaptive_concurrency:
        enabled: true
        min_concurrency: 20
        max_concurrency: 500
```

Per-route fields fall back to global values when set to zero.

## Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable adaptive concurrency limiting |
| `min_concurrency` | int | 5 | Minimum concurrency limit (floor for AIMD) |
| `max_concurrency` | int | 1000 | Maximum concurrency limit (initial value) |
| `latency_tolerance` | float | 2.0 | Gradient threshold for decrease (must be >= 1.0) |
| `adjustment_interval` | duration | 5s | How often the limit is recalculated |
| `smoothing_factor` | float | 0.5 | EWMA alpha (0 < alpha < 1, higher = more responsive) |
| `min_latency_samples` | int | 25 | Minimum samples before adjustments begin |

## Middleware Position

Adaptive concurrency sits at position 12.5 in the middleware chain, between the circuit breaker (12) and compression (13). This means:

- Circuit breaker handles hard failures (backend down) before the limiter
- When the circuit breaker is open, requests never reach the adaptive limiter
- Measured latency includes compression, response rules, mirror, transforms, and the proxy round-trip

## Interactions with Other Features

**Circuit Breaker**: Complementary. Circuit breaker reacts to failures; adaptive concurrency reacts to latency degradation. The circuit breaker sits before the adaptive limiter in the chain.

**Rate Limiting / Throttle**: Rate limiting and throttle control request rate (requests/second). Adaptive concurrency controls concurrent requests in-flight. They can be combined.

**Priority Admission**: Priority admission uses a fixed concurrency limit with queue ordering. Adaptive concurrency dynamically adjusts its limit without queuing. Use priority when you need ordering guarantees; use adaptive concurrency when you want automatic limit discovery.

## Admin API

`GET /adaptive-concurrency` returns per-route statistics:

```json
{
  "api": {
    "current_limit": 47,
    "in_flight": 12,
    "ewma_latency_ms": 23.5,
    "min_latency_ms": 15.2,
    "samples": 1523,
    "total_requests": 5000,
    "total_admitted": 4800,
    "total_rejected": 200
  }
}
```

Adaptive concurrency stats also appear in `GET /traffic-shaping` and `GET /dashboard`.
