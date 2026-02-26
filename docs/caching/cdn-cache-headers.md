---
title: "CDN Cache Headers"
sidebar_position: 3
---

Inject Cache-Control, Vary, Surrogate-Control, and related headers for CDN and browser caching integration.

## Configuration

### Per-Route

```yaml
routes:
  - id: public-api
    path: /api/public
    backends:
      - url: http://backend:8080
    cdn_cache_headers:
      enabled: true
      cache_control: "public, max-age=3600, s-maxage=86400"
      vary:
        - Accept
        - Accept-Encoding
      surrogate_control: "max-age=86400"
      surrogate_key: "public-api"
```

### Global

```yaml
cdn_cache_headers:
  enabled: true
  cache_control: "public, max-age=60"
  vary:
    - Accept-Encoding

routes:
  - id: static-assets
    path: /static/*path
    backends:
      - url: http://cdn-origin:8080
    cdn_cache_headers:
      enabled: true
      cache_control: "public, max-age=31536000, immutable"
```

Per-route config takes precedence over global when enabled.

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable CDN cache header injection |
| `cache_control` | string | | Cache-Control header value |
| `vary` | list | | Vary header values |
| `surrogate_control` | string | | Surrogate-Control header (CDN-specific) |
| `surrogate_key` | string | | Surrogate-Key for targeted purging |
| `expires` | string | | Expires as duration (`1h`) or HTTP-date |
| `stale_while_revalidate` | int | 0 | Seconds appended to Cache-Control |
| `stale_if_error` | int | 0 | Seconds appended to Cache-Control |
| `override` | bool | true | Override backend's Cache-Control |

## Override Behavior

By default (`override: true`), the runway replaces any Cache-Control header set by the backend. Set `override: false` to only inject headers when the backend doesn't provide its own:

```yaml
cdn_cache_headers:
  enabled: true
  cache_control: "public, max-age=300"
  override: false  # only set if backend didn't provide Cache-Control
```

## Stale Directives

`stale_while_revalidate` and `stale_if_error` are appended to the `cache_control` value:

```yaml
cdn_cache_headers:
  enabled: true
  cache_control: "public, max-age=3600"
  stale_while_revalidate: 300
  stale_if_error: 600
```

Produces: `Cache-Control: public, max-age=3600, stale-while-revalidate=300, stale-if-error=600`

## Middleware Position

Step 4.07 in the middleware chain — after security headers (4.05), before edge cache rules (4.08) and error pages (4.1). Headers are injected when the response is written.

For conditional cache-control rules that vary based on response status code, content type, or path patterns, see [Edge Cache Rules](./edge-cache-rules.md). Edge cache rules run after CDN cache headers and can override them when both are configured.

## Admin API

`GET /cdn-cache-headers` returns per-route CDN header stats:

```json
{
  "public-api": {
    "applied": 1500,
    "cache_control": "public, max-age=3600, s-maxage=86400",
    "vary": "Accept, Accept-Encoding",
    "surrogate_control": "max-age=86400",
    "override": true
  }
}
```
