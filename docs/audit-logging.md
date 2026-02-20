# Audit Logging

Audit logging provides a structured record of API requests and responses, delivered asynchronously to a webhook endpoint. Unlike access logs (which write to local log files), audit logs are designed for compliance, security monitoring, and forensics use cases where events must be delivered to an external system.

## Overview

Audit logging captures request and response metadata (method, path, status, headers, identity) and optionally includes request/response bodies. Events are buffered internally and delivered in batches to a configured webhook URL, providing reliable delivery without impacting request latency.

Key features:
- Asynchronous delivery via buffered channel and batch flush
- Configurable sampling rate to control volume
- Method and status code filtering
- Optional body capture with size limits
- Per-route overrides of global audit configuration
- Merge semantics: per-route config overrides global config

## Configuration

### Global

```yaml
audit_log:
  enabled: true
  webhook_url: "https://audit.example.com/events"
  headers:
    Authorization: "Bearer audit-token"
    X-Source: "api-gateway"
  sample_rate: 1.0
  include_body: false
  max_body_size: 65536          # 64KB
  buffer_size: 1000
  batch_size: 10
  flush_interval: 5s
  methods: ["POST", "PUT", "DELETE", "PATCH"]
  status_codes: [400, 401, 403, 500]
```

### Per-Route

Per-route config overrides global settings. Fields that are not set in the per-route config inherit from the global config.

```yaml
routes:
  - id: "payments"
    path: "/api/payments"
    path_prefix: true
    backends:
      - url: "http://payment-service:9000"
    audit_log:
      enabled: true
      include_body: true
      max_body_size: 16384       # 16KB for this route
      sample_rate: 1.0           # audit every request (no sampling)
      methods: []                # all methods (override global filter)
      status_codes: []           # all status codes (override global filter)
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable audit logging |
| `webhook_url` | string | required | URL to POST audit events to |
| `headers` | map[string]string | `{}` | HTTP headers to include on webhook requests |
| `sample_rate` | float | `1.0` | Fraction of requests to audit (0.0 = none, 1.0 = all). Must be between 0 and 1 inclusive. |
| `include_body` | bool | `false` | Capture request and response bodies in audit events |
| `max_body_size` | int | `65536` | Maximum body size to capture in bytes (64KB). Bodies larger than this are truncated. |
| `buffer_size` | int | `1000` | Internal event buffer capacity. When full, new events are dropped (with a warning log). |
| `batch_size` | int | `10` | Number of events per webhook delivery batch |
| `flush_interval` | duration | `5s` | Maximum time between webhook deliveries. A batch is sent when either `batch_size` events accumulate or `flush_interval` elapses, whichever comes first. |
| `methods` | []string | `[]` | HTTP methods to audit. Empty list means all methods. |
| `status_codes` | []int | `[]` | HTTP status codes to audit. Empty list means all status codes. |

## Pipeline Position

Audit logging runs at step 4.3 in the per-route middleware chain, after the access log middleware and before versioning:

```
... → accessLogMW (4.25) → auditLogMW (4.3) → versioningMW (4.5) → ...
```

This position ensures that:
- The request ID and variable context are available
- Access logging and audit logging are independent (both can be enabled simultaneously)
- The audit middleware can wrap the response writer to capture the status code and response body

## Webhook Delivery

### Event Format

Audit events are delivered as JSON arrays (batches) via HTTP POST:

```json
[
  {
    "timestamp": "2026-02-20T10:30:00.123Z",
    "request_id": "abc-123-def",
    "route_id": "payments",
    "client_ip": "203.0.113.50",
    "method": "POST",
    "path": "/api/payments",
    "query": "currency=USD",
    "status_code": 201,
    "latency_ms": 45,
    "user_agent": "PaymentApp/2.1",
    "identity": {
      "client_id": "merchant-42",
      "subject": "user@example.com"
    },
    "request_headers": {
      "Content-Type": "application/json",
      "X-Correlation-ID": "corr-789"
    },
    "response_headers": {
      "Content-Type": "application/json"
    },
    "request_body": "{\"amount\":99.99,\"currency\":\"USD\"}",
    "response_body": "{\"id\":\"pay_abc\",\"status\":\"created\"}"
  }
]
```

The `identity` field is populated when authentication is configured. `request_body` and `response_body` are only present when `include_body: true`.

### Delivery semantics

- Events are buffered in an internal channel of capacity `buffer_size`.
- A background goroutine flushes batches of up to `batch_size` events, or on `flush_interval` timeout.
- Webhook requests include the configured `headers` and `Content-Type: application/json`.
- If the webhook endpoint returns a non-2xx status, the batch is logged as a warning and dropped.
- If the buffer is full, new events are dropped with a warning log. Increase `buffer_size` if drops occur.

## Admin API

### GET `/audit-log`

Returns per-route audit logging configuration and delivery metrics.

```bash
curl http://localhost:8081/audit-log
```

**Response (200 OK):**
```json
{
  "payments": {
    "enabled": true,
    "webhook_url": "https://audit.example.com/events",
    "sample_rate": 1.0,
    "include_body": true,
    "max_body_size": 16384,
    "buffer_size": 1000,
    "batch_size": 10,
    "flush_interval": "5s",
    "methods": [],
    "status_codes": [],
    "events_captured": 5200,
    "events_delivered": 5180,
    "events_dropped": 0,
    "delivery_errors": 2
  }
}
```

| Field | Description |
|-------|-------------|
| `events_captured` | Total events captured (after sampling and filtering) |
| `events_delivered` | Total events successfully delivered to webhook |
| `events_dropped` | Events dropped due to full buffer |
| `delivery_errors` | Webhook delivery failures |

## Examples

### Compliance audit for all mutations

Capture all write operations for compliance:

```yaml
audit_log:
  enabled: true
  webhook_url: "https://siem.internal/api/events"
  headers:
    Authorization: "Bearer ${AUDIT_TOKEN}"
  methods: ["POST", "PUT", "DELETE", "PATCH"]
  include_body: true
  max_body_size: 65536
  buffer_size: 5000
  batch_size: 50
  flush_interval: 10s
```

### Security monitoring with sampling

High-traffic routes can use sampling to reduce volume while still catching anomalies:

```yaml
audit_log:
  enabled: true
  webhook_url: "https://security.internal/audit"
  sample_rate: 0.1           # audit 10% of requests
  status_codes: [401, 403, 429, 500, 502, 503]
  buffer_size: 2000
```

### Per-route override for sensitive endpoints

Override global config for payment and admin endpoints:

```yaml
audit_log:
  enabled: true
  webhook_url: "https://audit.example.com/events"
  sample_rate: 0.1
  include_body: false

routes:
  - id: "payments"
    path: "/api/payments"
    path_prefix: true
    backends:
      - url: "http://payment-service:9000"
    audit_log:
      sample_rate: 1.0          # audit every payment request
      include_body: true         # capture payment bodies
      max_body_size: 32768

  - id: "admin"
    path: "/admin"
    path_prefix: true
    backends:
      - url: "http://admin-service:9000"
    audit_log:
      sample_rate: 1.0
      include_body: true
      methods: []               # all methods
      status_codes: []          # all status codes

  - id: "public"
    path: "/public"
    path_prefix: true
    backends:
      - url: "http://public-service:9000"
    audit_log:
      enabled: false            # no audit for public content
```

### High-throughput buffering

For high-traffic gateways, tune buffer and batch settings:

```yaml
audit_log:
  enabled: true
  webhook_url: "https://audit.example.com/bulk"
  buffer_size: 10000
  batch_size: 100
  flush_interval: 30s
  sample_rate: 0.05            # 5% sampling
```
