---
title: "Edge Cache Rules"
sidebar_position: 4
---

Conditional cache-control rules that dynamically set Cache-Control and Vary headers based on response status code, content type, and request path patterns.

## Configuration

### Per-Route

```yaml
routes:
  - id: api
    path: /api
    path_prefix: true
    backends:
      - url: http://backend:8080
    edge_cache_rules:
      enabled: true
      rules:
        - match:
            status_codes: [200]
            content_types: ["application/json"]
          s_maxage: 300
          max_age: 60
          vary: ["Accept", "Authorization"]
        - match:
            status_codes: [200]
            path_patterns: ["/api/static/*"]
          s_maxage: 86400
          max_age: 3600
        - match:
            status_codes: [401, 403]
          no_store: true
```

### Global

```yaml
edge_cache_rules:
  enabled: true
  rules:
    - match:
        status_codes: [200, 301]
      s_maxage: 600
      max_age: 120
```

Per-route config takes precedence over global when enabled.

## How It Works

Rules are evaluated in order on every response. The first matching rule wins:

1. **Status code** — if specified, the response status must be in the list
2. **Content type** — if specified, the response Content-Type must match (prefix matching handles charset variants like `text/html; charset=utf-8`)
3. **Path pattern** — if specified, the request path must match at least one glob pattern

All specified conditions must match (AND logic). Unspecified conditions are skipped.

## Rule Actions

Each rule can set Cache-Control using either a raw value or structured fields:

| Field | Type | Description |
|-------|------|-------------|
| `cache_control` | string | Raw Cache-Control value (overrides all other fields) |
| `s_maxage` | int | Shared cache TTL in seconds (CDN) |
| `max_age` | int | Private cache TTL in seconds (browser) |
| `no_store` | bool | Set `Cache-Control: no-store` |
| `private` | bool | Set `Cache-Control: private` (instead of `public`) |
| `vary` | list | Set Vary header |
| `override` | bool | Override backend's Cache-Control (default true) |

When using structured fields (not `cache_control`), the value is assembled as:
- `no_store: true` produces `no-store`
- Otherwise: `public` (or `private`) + `max-age=N` + `s-maxage=N`

## Common Patterns

### Immutable static assets

```yaml
edge_cache_rules:
  enabled: true
  rules:
    - match:
        path_patterns: ["/static/*", "/assets/*"]
        status_codes: [200]
      cache_control: "public, max-age=31536000, immutable"
```

### No-store for authenticated responses

```yaml
edge_cache_rules:
  enabled: true
  rules:
    - match:
        status_codes: [401, 403]
      no_store: true
    - match:
        status_codes: [200]
        path_patterns: ["/api/me", "/api/settings"]
      private: true
      max_age: 0
```

### Separate CDN and browser TTLs

```yaml
edge_cache_rules:
  enabled: true
  rules:
    - match:
        status_codes: [200]
        content_types: ["text/html"]
      s_maxage: 3600    # CDN caches for 1 hour
      max_age: 60       # browser caches for 1 minute
      vary: ["Accept-Encoding"]
    - match:
        status_codes: [200]
        content_types: ["application/json"]
      s_maxage: 300
      max_age: 0
```

## Override Behavior

By default (`override: true`), matching rules replace any Cache-Control header set by the backend. Set `override: false` to only apply the rule when the backend didn't set Cache-Control:

```yaml
rules:
  - match:
      status_codes: [200]
    s_maxage: 300
    override: false  # respect backend's Cache-Control if present
```

## Relationship to CDN Cache Headers

Edge cache rules and [CDN cache headers](./cdn-cache-headers.md) can both be configured on the same route. Edge cache rules (step 4.08) run after CDN cache headers (step 4.07), so edge cache rules can override static CDN headers when conditions match. Use CDN cache headers for simple static values and edge cache rules for conditional logic.

## Middleware Position

Step 4.08 in the middleware chain — after CDN cache headers (4.07), before error pages (4.1). Headers are set when the response is written (via a wrapping ResponseWriter).

## Admin API

`GET /edge-cache-rules` returns per-route stats:

```json
{
  "api": {
    "applied": 500,
    "rule_count": 3
  }
}
```
