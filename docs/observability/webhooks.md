---
title: "Event Webhooks"
sidebar_position: 2
---

The runway can push real-time notifications to external systems when significant events occur: circuit breakers tripping, backends going unhealthy, canary rollbacks, or config reload failures.

## Overview

Webhooks are **runway-wide** (not per-route). A background worker pool asynchronously delivers HMAC-SHA256 signed HTTP POST requests to configured endpoints. Failed deliveries are retried with exponential backoff.

## Configuration

```yaml
webhooks:
  enabled: true
  timeout: 5s         # HTTP request timeout per delivery
  workers: 4          # background worker goroutines (default 4)
  queue_size: 1000    # event queue capacity (default 1000)
  retry:
    max_retries: 3    # retries on 5xx/network error (default 3)
    backoff: 1s       # initial backoff (default 1s)
    max_backoff: 30s  # max backoff cap (default 30s)
  endpoints:
    - id: "ops-alerts"
      url: "https://hooks.example.com/runway"
      secret: "whsec_abc123"
      events:
        - "backend.unhealthy"
        - "circuit_breaker.state_change"
        - "canary.rolled_back"
        - "config.reload_failure"
    - id: "audit-log"
      url: "https://audit.example.com/events"
      events:
        - "canary.*"
        - "config.*"
      routes:
        - "payments-api"
```

## Event Types

| Event | Description |
|-------|-------------|
| `backend.healthy` | Backend transitioned to healthy |
| `backend.unhealthy` | Backend transitioned to unhealthy |
| `circuit_breaker.state_change` | Circuit breaker changed state (closed/open/half-open) |
| `canary.started` | Canary deployment started |
| `canary.paused` | Canary deployment paused |
| `canary.resumed` | Canary deployment resumed |
| `canary.promoted` | Canary promoted to 100% |
| `canary.rolled_back` | Canary rolled back (includes reason) |
| `canary.step_advanced` | Canary advanced to next weight step |
| `canary.completed` | Canary completed all steps |
| `outlier.ejected` | Backend ejected by outlier detection (includes backend URL and reason) |
| `outlier.recovered` | Backend recovered from outlier ejection |
| `config.reload_success` | Configuration reload succeeded |
| `config.reload_failure` | Configuration reload failed (includes error) |

## Event Filtering

Each endpoint subscribes to specific event types via the `events` list:

- **Exact match**: `"backend.unhealthy"` matches only that event
- **Wildcard prefix**: `"canary.*"` matches any event starting with `canary.`
- **Match all**: `"*"` matches every event

### Route Filtering

The optional `routes` list restricts delivery to events from specific routes. When omitted, all routes match. Events without a route ID (like `config.*`) always match.

## Payload Format

```json
{
  "type": "circuit_breaker.state_change",
  "timestamp": "2026-02-09T12:34:56Z",
  "route_id": "api-v2",
  "data": {
    "from": "closed",
    "to": "open"
  }
}
```

## HTTP Headers

| Header | Description |
|--------|-------------|
| `Content-Type` | `application/json` |
| `X-Webhook-Event` | Event type (e.g., `circuit_breaker.state_change`) |
| `X-Webhook-Timestamp` | Unix timestamp (seconds) of delivery time |
| `X-Webhook-Signature` | `sha256=<hex_hmac>` (only when `secret` is set) |

Custom headers from the endpoint config are also included.

## HMAC Signing

When a `secret` is configured on an endpoint, the runway computes an HMAC-SHA256 signature over the raw JSON body using the secret as the key. The signature is sent in the `X-Webhook-Signature` header as `sha256=<hex>`.

To verify:
1. Read the raw request body
2. Compute `HMAC-SHA256(secret, body)`
3. Compare with the hex value after the `sha256=` prefix

The `X-Webhook-Timestamp` header provides replay protection.

## Retry Behavior

- **Success**: HTTP 2xx status
- **Retry**: HTTP 5xx or network error, with exponential backoff
- **No retry**: HTTP 4xx (client error)
- Backoff doubles each attempt: `backoff`, `backoff*2`, `backoff*4`, ... capped at `max_backoff`

## Non-Blocking Delivery

`Emit()` is non-blocking. If the queue is full, the event is dropped and the `total_dropped` metric incremented. The runway is never blocked by webhook delivery.

## Admin API

### GET `/webhooks`

Returns dispatcher stats, queue usage, delivery metrics, and recent events.

```json
{
  "enabled": true,
  "endpoints": 2,
  "queue_size": 1000,
  "queue_used": 3,
  "metrics": {
    "total_emitted": 150,
    "total_delivered": 145,
    "total_failed": 2,
    "total_dropped": 0,
    "total_retries": 5
  },
  "recent_events": [
    {
      "type": "backend.unhealthy",
      "timestamp": "2026-02-09T12:30:00Z",
      "route_id": "",
      "data": {"url": "http://backend:8080", "status": "unhealthy"}
    }
  ]
}
```

## Hot Reload

The webhook dispatcher persists across config reloads. Only the endpoint list is updated. In-flight deliveries complete normally. After reload, a `config.reload_success` or `config.reload_failure` event is emitted.
