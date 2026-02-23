# Response Streaming

The streaming middleware controls how response data is flushed to the client. By default, the Go reverse proxy buffers response data. This middleware provides options for immediate or periodic flushing, which is important for streaming APIs, server-sent events, and long-lived connections.

## Configuration

Per-route:

```yaml
routes:
  - id: "live-data"
    path: "/stream"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    streaming:
      enabled: true
      flush_interval: 100ms
```

Or for immediate flushing:

```yaml
routes:
  - id: "realtime"
    path: "/realtime"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    streaming:
      enabled: true
      disable_buffering: true
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `streaming.enabled` | bool | false | Enable streaming controls |
| `streaming.flush_interval` | duration | 0 | Flush response to client at this interval |
| `streaming.disable_buffering` | bool | false | Flush after every Write call |

## Behavior

- **`flush_interval`** -- A background ticker flushes buffered response data to the client at the specified interval. Useful for chunked transfer or SSE-like patterns where some buffering is acceptable.
- **`disable_buffering`** -- Every `Write()` call from the backend is immediately flushed to the client. This provides the lowest latency but highest overhead.

These two options are mutually exclusive. Set one or the other, not both.

## Admin Endpoint

`GET /streaming` returns per-route streaming configuration status.

```bash
curl http://localhost:8081/streaming
```

See [Configuration Reference](configuration-reference.md#streaming-per-route) for field details.
