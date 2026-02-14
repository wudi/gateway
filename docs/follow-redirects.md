# Follow Redirects

By default the gateway's reverse proxy uses `transport.RoundTrip()` which does **not** follow backend `3xx` redirects. Enabling follow redirects makes the gateway transparently chase `Location` headers before returning the final response to the client.

## How It Works

A `RedirectTransport` wraps the upstream transport. When a response status is 301, 302, 303, 307, or 308, the transport reads the `Location` header and issues a new request, up to `max_redirects` hops. Original request headers are copied to each redirect request. For `303 See Other`, the method is changed to `GET` (per RFC 7231). For `307`/`308`, the original method is preserved.

If the redirect limit is exceeded, the last `3xx` response is returned to the client as-is.

## Configuration

Follow redirects is configured per route on `RouteConfig`.

```yaml
routes:
  - id: legacy-api
    path: /legacy
    backends:
      - url: http://old-service:8080
    follow_redirects:
      enabled: true
      max_redirects: 5
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable following backend redirects |
| `max_redirects` | int | `10` | Maximum number of redirects to follow |

## Redirect Behavior by Status Code

| Status | Method Change | Body Forwarded |
|--------|--------------|----------------|
| 301 Moved Permanently | Preserved | No |
| 302 Found | Preserved | No |
| 303 See Other | Changed to GET | No |
| 307 Temporary Redirect | Preserved | No |
| 308 Permanent Redirect | Preserved | No |

## Admin API

```
GET /follow-redirects
```

Returns per-route redirect stats:
```json
{
  "legacy-api": {
    "redirects_followed": 150,
    "max_exceeded": 2,
    "max_redirects": 5
  }
}
```

## Example: Migration Proxy

Follow redirects from a legacy backend that issues multiple internal redirects:

```yaml
routes:
  - id: migration
    path: /v1
    backends:
      - url: http://legacy:3000
    follow_redirects:
      enabled: true
      max_redirects: 3
```
