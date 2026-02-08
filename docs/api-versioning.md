# API Versioning

The gateway supports automatic API versioning, allowing a single route to serve multiple API versions by routing to version-specific backend groups.

## Overview

API versioning detects the requested version from the incoming request (path prefix, header, Accept header, or query parameter), sets the version in the request context, and routes to the appropriate backend group. Deprecated versions can automatically include `Deprecation` and `Sunset` response headers.

## Configuration

```yaml
routes:
  - id: api
    path: /{path:*}
    path_prefix: true
    versioning:
      enabled: true
      source: path              # path, header, accept, query
      path_prefix: "/v"         # prefix to match (default: /v)
      strip_prefix: true        # remove /vN from forwarded path
      default_version: "2"
      versions:
        "1":
          backends:
            - url: http://api-v1:8080
          deprecated: true
          sunset: "2025-12-31"
        "2":
          backends:
            - url: http://api-v2:8080
```

## Version Detection Sources

### Path Prefix (`source: path`)

Extracts the version from the URL path. With `path_prefix: "/v"`, a request to `/v2/users` is detected as version `"2"`.

When `strip_prefix: true`, the `/v2` prefix is removed before forwarding, so the backend receives `/users`.

```yaml
versioning:
  source: path
  path_prefix: "/v"
  strip_prefix: true
```

### Header (`source: header`)

Reads the version from a request header.

```yaml
versioning:
  source: header
  header_name: "X-API-Version"   # default
```

Example request: `GET /users` with header `X-API-Version: 2`.

### Accept Header (`source: accept`)

Extracts the version from the `Accept` header using the `application/vnd.*.v(\d+)` pattern.

```yaml
versioning:
  source: accept
```

Example request: `GET /users` with `Accept: application/vnd.myapi.v2+json`.

### Query Parameter (`source: query`)

Reads the version from a query parameter.

```yaml
versioning:
  source: query
  query_param: "version"   # default
```

Example request: `GET /users?version=2`.

## Deprecation Headers

Versions marked as deprecated automatically add response headers:

```yaml
versions:
  "1":
    deprecated: true
    sunset: "2025-12-31"
```

Responses for version 1 will include:
```
Deprecation: true
Sunset: 2025-12-31
```

## Variables

The detected API version is available as `api_version` in the variables context, accessible in transforms, rules, and logging.

## Mutual Exclusivity

- `versioning` and `traffic_split` cannot be used on the same route (both control backend selection)
- `versioning` and top-level `backends` cannot be used on the same route (backends are defined inside versions)

## Admin API

### `GET /versioning`

Returns per-route versioning stats:

```json
{
  "api": {
    "source": "path",
    "default_version": "2",
    "versions": {
      "1": {
        "requests": 150,
        "deprecated": true,
        "sunset": "2025-12-31"
      },
      "2": {
        "requests": 4200,
        "deprecated": false
      }
    },
    "unknown_count": 3
  }
}
```
