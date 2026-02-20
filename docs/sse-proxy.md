# Server-Sent Events (SSE) Proxy

The gateway provides SSE-aware proxying with per-event flushing, heartbeat injection, and connection management. When a backend responds with `Content-Type: text/event-stream`, the SSE middleware takes over response writing to ensure proper streaming behavior.

## Configuration

```yaml
routes:
  - id: events-api
    path: /events
    backends:
      - url: http://backend:8080
    sse:
      enabled: true
      heartbeat_interval: 30s
      retry_ms: 3000
      connect_event: "connected"
      disconnect_event: "disconnected"
      max_idle: 5m
      forward_last_event_id: true
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable SSE proxy mode for this route |
| `heartbeat_interval` | duration | `0` (disabled) | Send `: heartbeat\n\n` comment when idle for this duration |
| `retry_ms` | int | `0` (don't inject) | Inject `retry: <ms>\n\n` field on SSE connection start |
| `connect_event` | string | `""` (none) | Event data to send when SSE connection is established |
| `disconnect_event` | string | `""` (none) | Event data to send when SSE connection closes |
| `max_idle` | duration | `0` (no limit) | Close connection after this idle duration |
| `forward_last_event_id` | bool | `true` | Forward the client's `Last-Event-ID` header to the backend |

## How It Works

The SSE middleware sits at step 10.5 in the middleware chain (after WebSocket at step 10, before cache at step 11). It wraps the `ResponseWriter` and inspects the backend's response:

1. **Non-SSE responses** pass through with zero overhead. The wrapper is transparent.
2. **SSE responses** (`Content-Type: text/event-stream`) activate SSE mode:
   - Sets `Cache-Control: no-store` and removes `Content-Length` to prevent downstream buffering
   - Buffers writes until a complete SSE event boundary (`\n\n`) is found
   - Flushes each complete event individually for real-time streaming
   - Tracks active connections and event counts

### Event-Aware Flushing

Standard HTTP proxying buffers response data, which breaks SSE streaming. The SSE middleware scans each `Write()` call for `\n\n` boundaries (the SSE event delimiter) and flushes after each complete event. Partial events are buffered until the boundary arrives.

### Heartbeat

When `heartbeat_interval` is set, the middleware sends SSE comment lines (`: heartbeat\n\n`) during idle periods. This keeps the connection alive through proxies and load balancers that may close idle connections.

### Reconnection

The SSE specification supports automatic reconnection via the `retry:` field. When `retry_ms` is configured, the middleware injects `retry: <ms>\n\n` at the start of each SSE connection. Browsers will use this value as the reconnection delay.

When `forward_last_event_id` is enabled (default), the client's `Last-Event-ID` header is forwarded to the backend, allowing it to resume the event stream from where the client left off.

## Event Injection

### Connect Event

When `connect_event` is set, the middleware sends a `data: <value>\n\n` event immediately after the SSE connection is established (after any `retry:` field). This can be used to confirm the connection or send initial state.

### Disconnect Event

When `disconnect_event` is set, the middleware sends a `data: <value>\n\n` event just before the SSE connection closes. This fires when the handler completes or the client disconnects.

## Compatibility

The SSE middleware prevents buffering by downstream middleware:

- **Cache**: SSE responses have `Cache-Control: no-store` injected, so cache middleware skips them
- **Compression**: Works correctly — compression middleware sees the streaming writes
- **Response rules**: May evaluate on incomplete data; consider disabling for SSE routes

## Middleware Position

Step 10.5 — after WebSocket (10), before cache (11). This ensures:

- WebSocket upgrades are handled first (step 10)
- SSE wrapper is in place before cache (11), coalescing (11.5), and other buffering middleware
- Auth, rate limiting, and IP filter all run before SSE (correct)

## Admin API

```
GET /sse
```

Returns per-route SSE proxy statistics:

```json
{
  "events-api": {
    "active_connections": 5,
    "total_connections": 142,
    "total_events": 8923,
    "heartbeats_sent": 456
  }
}
```

## Validation Rules

- `heartbeat_interval` must be >= 0
- `retry_ms` must be >= 0
- `max_idle` must be >= 0
- SSE and `passthrough` are mutually exclusive
- SSE and `response_body_generator` are mutually exclusive
