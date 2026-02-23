# API Deprecation Lifecycle

The gateway supports RFC 8594 API deprecation headers, allowing you to signal to consumers that an API is deprecated and will be sunset on a specific date. After the sunset date, the gateway can optionally block requests entirely.

## Configuration

Per-route deprecation:

```yaml
routes:
  - id: "legacy-api"
    path: "/api/v1"
    backends:
      - url: "http://backend:8080"
    deprecation:
      enabled: true
      sunset_date: "2025-06-01T00:00:00Z"    # RFC 3339 format
      message: "Use /api/v2 instead"
      link: "https://docs.example.com/api/v2"
      link_relation: "successor-version"       # default
      log_level: "warn"                        # "warn" (default) or "info"
      response_after_sunset:
        status: 410
        body: '{"error":"This API has been sunset. Please migrate to /api/v2."}'
        headers:
          Content-Type: "application/json"
```

Global deprecation (applies to all routes):

```yaml
deprecation:
  enabled: true
  message: "This gateway is being deprecated"
  sunset_date: "2025-12-31T00:00:00Z"
```

## Headers

The middleware always injects:

- **`Deprecation: true`** — Signals the API is deprecated (RFC 8594)
- **`Sunset: <HTTP-date>`** — When the API will be removed (RFC 7231 date format), if `sunset_date` is configured
- **`Link: <url>; rel="successor-version"`** — URL to the replacement API, if `link` is configured

## Sunset Blocking

When `response_after_sunset` is configured and the current time is past `sunset_date`, the gateway blocks the request and returns the configured response. The default status is 410 (Gone).

Deprecation headers are still included on the blocked response.

## Logging

Every request to a deprecated route is logged. Set `log_level` to control severity:
- `"warn"` (default) — Logs at warning level
- `"info"` — Logs at info level

## Middleware Position

The deprecation middleware runs at step **4.55** in the middleware chain:
- After versioning (4.5) — version resolution happens first
- Before rate limiting (5.0) — sunset blocking doesn't consume rate limit tokens
- Before auth (6.0) — blocking a sunset route doesn't require authentication

## Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `deprecation.enabled` | bool | Enable deprecation headers |
| `deprecation.sunset_date` | string | RFC 3339 date when API will be removed |
| `deprecation.message` | string | Human-readable deprecation notice |
| `deprecation.link` | string | URL to replacement API |
| `deprecation.link_relation` | string | Link relation type (default "successor-version") |
| `deprecation.log_level` | string | "warn" (default) or "info" |
| `deprecation.response_after_sunset.status` | int | HTTP status after sunset (default 410) |
| `deprecation.response_after_sunset.body` | string | Response body after sunset |
| `deprecation.response_after_sunset.headers` | map | Extra response headers after sunset |

## Admin API

- **GET** `/deprecation` — Returns per-route deprecation stats including request counts, blocked counts, and sunset status.
