# Request Queuing

Request queuing provides a bounded FIFO queue that absorbs traffic spikes instead of immediately rejecting requests. When all queue slots are occupied, new requests wait up to a configurable timeout before receiving a 503 response.

## Relationship to Rate Limiting and Throttle

Request queuing complements the existing traffic control stack:

- **Rate limiting** (step 5) — hard rejection of requests that exceed a rate
- **Spike arrest** (step 5.25) — smooth out bursts with fixed-window limiting
- **Throttle** (step 5.5) — token bucket with a short queue wait
- **Request queue** (step 5.75) — bounded FIFO that absorbs larger bursts

Rate limiting and throttle reject clearly over-limit traffic first. The request queue then absorbs legitimate bursts that pass those checks, protecting downstream authentication and backend resources during spikes.

## Configuration

Request queuing is configured under `traffic_shaping.request_queue` at both the global and per-route level. Per-route settings override global settings via `MergeNonZero`.

### Global

```yaml
traffic_shaping:
  request_queue:
    enabled: true
    max_depth: 200    # max queued requests
    max_wait: 30s     # max time a request waits in queue
```

### Per-Route

```yaml
routes:
  - id: api
    path: /api/
    backends:
      - url: http://backend:8080
    traffic_shaping:
      request_queue:
        enabled: true
        max_depth: 50     # override global for this route
        max_wait: 10s
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable request queuing |
| `max_depth` | int | `100` | Maximum number of requests that can be queued |
| `max_wait` | duration | `30s` | Maximum time a request waits in the queue before 503 |

## Behavior

1. When a request arrives, it attempts to acquire a queue slot (buffered channel semaphore).
2. If a slot is immediately available, the request proceeds to the next middleware.
3. If all slots are occupied, the request blocks up to `max_wait`.
4. If a slot becomes available within the timeout, the request proceeds.
5. If the timeout expires, the request receives a `503 Service Unavailable` response.
6. If the client cancels the request while waiting, it is counted as rejected.

## Metrics

The admin API exposes queue metrics at `GET /request-queues`:

```json
{
  "api": {
    "max_depth": 50,
    "max_wait_ms": 10000,
    "current_depth": 3,
    "enqueued": 1500,
    "dequeued": 1490,
    "rejected": 2,
    "timed_out": 8,
    "avg_wait_ms": 12.5
  }
}
```

| Metric | Description |
|--------|-------------|
| `current_depth` | Number of requests currently in the queue |
| `enqueued` | Total requests that entered the queue |
| `dequeued` | Total requests that completed processing |
| `rejected` | Requests rejected due to client cancellation |
| `timed_out` | Requests that exceeded `max_wait` |
| `avg_wait_ms` | Average queue wait time in milliseconds |

## Admin Endpoint

| Method | Path | Description |
|--------|------|-------------|
| GET | `/request-queues` | Returns queue metrics for all routes |
