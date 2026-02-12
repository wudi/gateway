# URL Rewriting

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
| `prefix` + `regex` | Mutually exclusive |
| `prefix` + `strip_prefix` | Mutually exclusive |
| `prefix` requires `path_prefix: true` | Prefix rewrite only applies to prefix-matched routes |
| `regex` requires `replacement` | Both must be specified together |
| `replacement` requires `regex` | Both must be specified together |
| `regex` must be valid | Validated at config load time |
| `host` is independent | Can be used alone or combined with path rewriting |
