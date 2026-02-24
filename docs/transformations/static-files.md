---
title: "Static File Serving"
sidebar_position: 15
---

Serve static files directly from the gateway for specific routes. The static file handler replaces the reverse proxy as the innermost handler — requests never reach a backend.

## Configuration

```yaml
routes:
  - id: docs-site
    path: /docs/
    path_prefix: true
    static:
      enabled: true
      root: /var/www/docs
      index: index.html          # default
      browse: false              # directory listing disabled by default
      cache_control: "public, max-age=3600"
```

## How It Works

When `static.enabled` is true for a route, the gateway serves files from the specified `root` directory using Go's built-in `http.FileServer`. The static handler replaces the proxy as the innermost handler in the middleware chain — all upstream middleware (auth, rate limiting, WAF, etc.) still applies.

### Path Resolution

- Request paths are cleaned and resolved relative to `root`
- Path traversal attempts (`..`) are rejected with 403 Forbidden

### Directory Access

- If a directory is requested and contains the `index` file (default `index.html`), it is served
- If no index file exists and `browse: false` (default), a 403 Forbidden is returned
- If `browse: true`, a directory listing is served

### Cache Control

When `cache_control` is set, the value is added as a `Cache-Control` response header on all served files.

## Mutual Exclusions

Static routes cannot use:
- `backends`, `service`, or `upstream` (no backend needed)
- `echo` (another no-backend handler)

Static routes **can** use all other middleware: auth, rate limiting, CORS, IP filtering, WAF, request rules, etc.

## Admin API

**GET `/static-files`** — returns per-route static file stats:

```json
{
  "docs-site": {
    "root": "/var/www/docs",
    "served": 1234,
    "browse": false
  }
}
```

## Validation

- `root` is required when enabled and must be an existing directory
- Mutually exclusive with `echo`, `backends`, `service`, and `upstream`
