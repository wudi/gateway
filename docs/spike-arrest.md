# Spike Arrest

Spike arrest provides continuous rate enforcement with immediate rejection. Unlike windowed rate limiting (which allows bursts within a window), spike arrest smooths traffic by enforcing a maximum request rate with a configurable burst tolerance.

## How It Works

A token bucket limiter (`x/time/rate`) fills at `rate/period` tokens per second, with a maximum of `burst` tokens. Each request consumes one token. When no tokens are available, the request is immediately rejected with `429 Too Many Requests` -- there is no queuing or delay (unlike throttling).

Spike arrest can operate globally per route or per client IP.

## Configuration

Spike arrest is configured on both the global `Config` and per-route `RouteConfig`. Per-route settings override global defaults using merge semantics.

### Global defaults

```yaml
spike_arrest:
  enabled: true
  rate: 100
  period: 1s
  burst: 50
```

### Per-route override

```yaml
routes:
  - id: api
    path: /api
    spike_arrest:
      enabled: true
      rate: 20
      period: 1s
      burst: 10
      per_ip: true
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable spike arrest |
| `rate` | int | - | Maximum requests per period |
| `period` | duration | `1s` | Time window for rate calculation |
| `burst` | int | same as `rate` | Maximum burst capacity |
| `per_ip` | bool | `false` | Track rate limits per client IP |

## Merge Behavior

When both global and per-route configs are present:

- `rate`: per-route wins if > 0
- `period`: per-route wins if > 0
- `burst`: per-route wins if > 0
- `per_ip`: per-route wins if true

A route with `enabled: true` activates spike arrest even if the global config is disabled.

## Per-IP Mode

When `per_ip: true`, each client IP gets its own rate limiter. Stale entries (no requests for 5 minutes) are automatically cleaned up. Client IP is extracted using the trusted proxies / real IP extractor if configured.

## Middleware Position

Step 5.25 in the per-route middleware chain -- after rate limiting (step 5) and before throttling (step 5.5).

## Admin API

```
GET /spike-arrest
```

Returns per-route stats:
```json
{
  "api": {
    "allowed": 5000,
    "rejected": 120,
    "per_ip": true,
    "tracked_ips": 45
  }
}
```

## Example: API Protection

Limit each IP to 10 requests/second with a burst of 15:

```yaml
routes:
  - id: public-api
    path: /api/v1
    spike_arrest:
      enabled: true
      rate: 10
      period: 1s
      burst: 15
      per_ip: true
```
