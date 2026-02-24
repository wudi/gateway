---
title: "URL Rewriting"
sidebar_position: 8
---

URL rewriting transforms the request path and/or Host header before proxying to backends. This is configured per-route via the `rewrite` block.

## Prefix Rewriting

Replace a matched path prefix with a different prefix. Requires `path_prefix: true` on the route.

```yaml
routes:
  - id: api-rewrite
    path: /api/v1
    path_prefix: true
    rewrite:
      prefix: /v2
    backends:
      - url: http://backend:8080
```

| Request Path | Backend Path |
|---|---|
| `/api/v1/users` | `/v2/users` |
| `/api/v1/users/123` | `/v2/users/123` |
| `/api/v1` | `/v2/` |

**Note:** `rewrite.prefix` and `strip_prefix` are mutually exclusive. Prefix rewrite subsumes strip_prefix functionality (use `prefix: /` to achieve the same effect as `strip_prefix: true`).

## Regex Rewriting

Apply a regex substitution on the request path. Supports capture group references (`$1`, `$2`, etc.).

```yaml
routes:
  - id: user-rewrite
    path: /users
    path_prefix: true
    rewrite:
      regex: "^/users/(\\d+)/posts$"
      replacement: "/api/v2/posts/$1"
    backends:
      - url: http://backend:8080
```

| Request Path | Backend Path |
|---|---|
| `/users/42/posts` | `/api/v2/posts/42` |
| `/users/999/posts` | `/api/v2/posts/999` |
| `/users/42/comments` | `/users/42/comments` (no match, unchanged) |

Both `regex` and `replacement` must be specified together. The regex is compiled at config load time and validated; invalid patterns cause a config error.

## Host Override

Override the `Host` header sent to the backend. This can be combined with either prefix or regex rewriting, or used alone.

```yaml
routes:
  - id: internal-route
    path: /api
    path_prefix: true
    rewrite:
      host: backend.internal.svc.cluster.local
    backends:
      - url: http://10.0.0.5:8080
```

The original request `Host` is preserved in the `X-Forwarded-Host` header (standard behavior).

## Full URL Override

Override the entire target URL -- scheme, host, port, path, and query string -- with a single `url` field. This takes precedence over all other rewrite modes (prefix, regex, strip_prefix). When `url` is set, the backend's original URL is completely replaced.

```yaml
routes:
  - id: full-override
    path: /legacy
    path_prefix: true
    rewrite:
      url: "https://other-host:8443/api/v2?key=abc"
    backends:
      - url: http://backend:8080
```

| Request Path | Actual Target URL |
|---|---|
| `/legacy/anything` | `https://other-host:8443/api/v2?key=abc` |
| `/legacy` | `https://other-host:8443/api/v2?key=abc` |

The `url` field replaces the scheme, host, port, path, and query of the proxied request. The original request path and query string are discarded. This is useful for routing to a completely different endpoint while preserving all gateway middleware (auth, rate limiting, etc.).

The `url` field can be combined with `host` (the `url` takes precedence for host), but combining it with `prefix` or `regex` is unnecessary since `url` overrides the entire target.

### Example: Cross-Service Redirect

Route a legacy path to an entirely different service and path:

```yaml
routes:
  - id: legacy-redirect
    path: /old-api/
    path_prefix: true
    rewrite:
      url: "https://new-service.internal:9443/v3/resources"
    backends:
      - url: http://placeholder:8080
```

All requests to `/old-api/*` are proxied to `https://new-service.internal:9443/v3/resources` regardless of the original path suffix.

## Combined Example

Prefix rewrite with host override:

```yaml
routes:
  - id: combined
    path: /public/api
    path_prefix: true
    rewrite:
      prefix: /internal/v3
      host: api-backend.internal
    backends:
      - url: http://10.0.0.5:8080
```

Request `GET /public/api/users` becomes `GET /internal/v3/users` with `Host: api-backend.internal`.

## Validation Rules

| Rule | Description |
|---|---|
| `url` takes precedence | When set, overrides prefix, regex, and strip_prefix behavior |
| `prefix` + `regex` | Mutually exclusive |
| `prefix` + `strip_prefix` | Mutually exclusive |
| `prefix` requires `path_prefix: true` | Prefix rewrite only applies to prefix-matched routes |
| `regex` requires `replacement` | Both must be specified together |
| `replacement` requires `regex` | Both must be specified together |
| `regex` must be valid | Validated at config load time |
| `host` is independent | Can be used alone or combined with path rewriting |
