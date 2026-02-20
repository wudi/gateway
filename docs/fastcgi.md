# FastCGI Proxy

The gateway can proxy requests to FastCGI backends such as PHP-FPM, enabling direct PHP application serving without an intermediate nginx layer.

## Overview

FastCGI support replaces the standard HTTP reverse proxy as the innermost handler for a route. Two modes are available:

- **Single-entry-point mode** — all requests are routed to a fixed script (e.g. `/index.php`), typical for frameworks like Laravel, Symfony, or WordPress.
- **Filesystem mode** — requests map to individual PHP files on disk, typical for classic PHP applications.

## Configuration

### Single-Entry-Point Mode

Route all requests through a single PHP script:

```yaml
routes:
  - id: laravel-app
    path: /
    path_prefix: true
    fastcgi:
      enabled: true
      address: "127.0.0.1:9000"
      document_root: /var/www/html/public
      script_name: /index.php
      params:
        HTTPS: "on"
```

### Filesystem Mode

Map request paths to individual PHP files:

```yaml
routes:
  - id: php-files
    path: /php/
    path_prefix: true
    fastcgi:
      enabled: true
      address: /run/php-fpm.sock
      document_root: /var/www/html
      index: index.php
      pool_size: 16
```

### Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable FastCGI proxying for this route |
| `address` | string | *required* | FastCGI backend address: `"host:port"` for TCP or `"/path/to.sock"` for Unix |
| `network` | string | auto-detected | `"tcp"` or `"unix"`; auto-detected from address if empty |
| `document_root` | string | *required* | Base directory for `DOCUMENT_ROOT` and `SCRIPT_FILENAME` |
| `script_name` | string | `""` | Fixed entry-point script (e.g. `"/index.php"`); empty enables filesystem mode |
| `index` | string | `"index.php"` | Default index file for filesystem mode |
| `conn_timeout` | duration | `5s` | Connection pool entry timeout |
| `read_timeout` | duration | `30s` | Read timeout for FastCGI responses |
| `params` | map | `{}` | Extra CGI parameters injected into every request |
| `pool_size` | int | `8` | Connection pool size |

### Network Auto-Detection

If `network` is omitted, the gateway infers it from the address:

- Addresses starting with `/` or ending with `.sock` use `"unix"`.
- All other addresses use `"tcp"`.

### CGI Parameters

The following CGI parameters are automatically set:

- Standard CGI params via `BasicParamsMap`: `REQUEST_METHOD`, `REQUEST_URI`, `QUERY_STRING`, `CONTENT_TYPE`, `CONTENT_LENGTH`, `SERVER_NAME`, `SERVER_PORT`, `SERVER_PROTOCOL`, `REMOTE_ADDR`, `REMOTE_PORT`, `REQUEST_SCHEME`
- HTTP headers mapped to `HTTP_*` params via `MapHeader`
- `DOCUMENT_ROOT` — from config
- `REDIRECT_STATUS` — set to `"200"` (required by php-cgi)
- `GATEWAY_INTERFACE` — `"CGI/1.1"`
- `SCRIPT_FILENAME` and `SCRIPT_NAME` — set by the endpoint middleware based on mode

Additional parameters can be injected via the `params` map.

## Mutual Exclusions

FastCGI replaces the proxy as the innermost handler. It is mutually exclusive with:

- `backends`, `service`, `upstream` (standard proxy targets)
- `echo` (echo handler)
- `static` (static file serving)
- `sequential` (sequential proxy)
- `aggregate` (response aggregation)
- `passthrough` (raw body passthrough)

## Admin API

| Endpoint | Description |
|----------|-------------|
| `GET /fastcgi` | Per-route FastCGI handler stats |

Example response:

```json
{
  "laravel-app": {
    "address": "127.0.0.1:9000",
    "network": "tcp",
    "document_root": "/var/www/html/public",
    "script_name": "/index.php",
    "total_requests": 1542,
    "total_errors": 0
  }
}
```
