---
title: "Traffic Replay"
sidebar_position: 6
---

Traffic replay allows you to record incoming HTTP requests on a per-route basis and replay them against a different backend. This is useful for migration validation, load testing, and verifying new backend versions handle the same traffic correctly.

## Configuration

Enable traffic replay on a route:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://production:8080"
    traffic_replay:
      enabled: true
      max_recordings: 10000    # ring buffer size (default 10000)
      percentage: 100          # sampling percentage 0-100 (default 100)
      max_body_size: 65536     # max body capture in bytes (default 64KB)
      conditions:
        methods:
          - "POST"
          - "PUT"
        path_regex: "^/api/v2/"
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable traffic replay for this route |
| `max_recordings` | int | 10000 | Maximum number of requests in the ring buffer |
| `percentage` | int | 100 | Percentage of matching requests to record (0-100) |
| `max_body_size` | int | 65536 | Maximum request body bytes to capture |
| `conditions.methods` | []string | all | Only record requests with these HTTP methods |
| `conditions.path_regex` | string | all | Only record requests matching this regex |

## How It Works

1. **Configure** the route with `traffic_replay.enabled: true`.
2. **Start recording** via the admin API. Incoming requests matching the conditions are captured into an in-memory ring buffer.
3. **Stop recording** when you have enough traffic captured.
4. **Replay** the captured traffic against a target backend via the admin API.
5. **Monitor** replay progress via the status endpoint.

The ring buffer wraps when full, keeping the most recent requests. Request bodies are captured up to `max_body_size` bytes.

## Admin API

All endpoints are under `/traffic-replay/{route}/`.

### Get Status

```
GET /traffic-replay/{route}/status
```

Returns recording state, buffer usage, and active replay stats:

```json
{
  "recording": true,
  "buffer_size": 10000,
  "buffer_used": 342,
  "total_count": 342,
  "replay": {
    "started": "2024-01-15T10:30:00Z",
    "total": 200,
    "sent": 150,
    "errors": 2,
    "completed": false
  }
}
```

### Start Recording

```
POST /traffic-replay/{route}/start
```

Enables request recording for the route.

### Stop Recording

```
POST /traffic-replay/{route}/stop
```

Disables request recording. Existing recordings are preserved.

### Trigger Replay

```
POST /traffic-replay/{route}/replay
Content-Type: application/json

{
  "target": "http://new-backend:8080",
  "concurrency": 10,
  "rate_per_sec": 100
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `target` | string | required | Target backend URL to replay against |
| `concurrency` | int | 10 | Number of concurrent replay workers |
| `rate_per_sec` | float | unlimited | Rate limit for replay requests per second |

Snapshots the current ring buffer and launches an asynchronous replay. The original recordings are not affected.

### Cancel Replay

```
POST /traffic-replay/{route}/cancel
```

Cancels an active replay operation.

### Clear Recordings

```
DELETE /traffic-replay/{route}/recordings
```

Clears all recorded requests from the ring buffer.

## Example Workflow

```bash
# 1. Start recording traffic on the api route
curl -X POST http://admin:9090/traffic-replay/api/start

# 2. Let traffic flow for a while...

# 3. Check how many requests were captured
curl http://admin:9090/traffic-replay/api/status

# 4. Stop recording
curl -X POST http://admin:9090/traffic-replay/api/stop

# 5. Replay against a new backend at 50 req/s
curl -X POST http://admin:9090/traffic-replay/api/replay \
  -H "Content-Type: application/json" \
  -d '{"target": "http://new-backend:8080", "concurrency": 5, "rate_per_sec": 50}'

# 6. Monitor replay progress
curl http://admin:9090/traffic-replay/api/status

# 7. Clear recordings when done
curl -X DELETE http://admin:9090/traffic-replay/api/recordings
```

## Middleware Position

Traffic replay recording sits at step 7.6 in the middleware chain, after authentication and request rules validation but before body-processing transforms. This ensures only valid, authenticated requests are captured in their original form.
