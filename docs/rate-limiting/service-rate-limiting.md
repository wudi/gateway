---
title: "Service-Level Rate Limiting"
sidebar_position: 2
---

Service-level rate limiting enforces a global throughput cap across the entire runway, regardless of route. It protects the runway process from being overwhelmed by total request volume.

## How It Works

A single token bucket rate limiter (`x/time/rate`) is applied in the global handler chain, after `RequestID` and before `Alt-Svc`. Every request consumes a token; when the bucket is empty, the request is immediately rejected with `429 Too Many Requests` and a `Retry-After` header.

This is different from per-route rate limiting, which limits individual routes independently.

## Configuration

```yaml
service_rate_limit:
  enabled: true
  rate: 10000        # requests per period
  period: 1s         # default: 1s
  burst: 15000       # burst capacity (default: same as rate)
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable service-level rate limiting |
| `rate` | int | - | Maximum sustained requests per period |
| `period` | duration | `1s` | Time window for rate calculation |
| `burst` | int | same as `rate` | Maximum burst capacity (token bucket size) |

## Behavior

- Rejected requests receive HTTP 429 with a JSON error body and `Retry-After: 1` header
- The limiter uses a token bucket algorithm, allowing short bursts up to `burst` while maintaining `rate/period` sustained throughput
- Applied before routing, so rejected requests never consume route-level resources

## Admin API

```
GET /service-rate-limit
```

Returns:
```json
{
  "enabled": true,
  "allowed": 150000,
  "rejected": 42
}
```

## Example

Limit the runway to 5000 requests/second with bursts up to 8000:

```yaml
service_rate_limit:
  enabled: true
  rate: 5000
  period: 1s
  burst: 8000
```
