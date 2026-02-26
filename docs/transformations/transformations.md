---
title: "Transformations"
sidebar_position: 1
---

The gateway can modify requests and responses as they pass through the proxy. Transformations include header manipulation, body modification, variable substitution, path rewriting, request validation, request decompression, and response compression.

## Header Transforms

Add, set, or remove headers on requests and responses:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    transform:
      request:
        headers:
          add:
            X-Request-ID: "$request_id"
            X-Forwarded-For: "$remote_addr"
            X-Forwarded-Proto: "$scheme"
          set:
            Host: "backend.internal"
          remove:
            - "X-Internal-Secret"
      response:
        headers:
          add:
            X-Request-ID: "$request_id"
            X-Response-Time: "$response_time ms"
          remove:
            - "X-Powered-By"
```

- **add**: appends a header (preserves existing values)
- **set**: replaces the header value entirely
- **remove**: deletes the header

## Body Transforms

Modify JSON request or response bodies. Transforms only apply to `application/json` content.

### Basic Operations

Add, remove, or rename top-level fields:

```yaml
transform:
  request:
    body:
      add_fields:
        source: "runway"
        timestamp: "$time_unix"
      remove_fields:
        - "internal_field"
      rename_fields:
        old_name: "new_name"
  response:
    body:
      remove_fields:
        - "debug_info"
```

### JSONPath-Based Operations

Use `set_fields` with dot-path notation to set nested fields, and dot-path `remove_fields` to remove nested fields:

```yaml
transform:
  request:
    body:
      set_fields:
        "metadata.source": "runway"
        "metadata.timestamp": "$time_unix"
      remove_fields:
        - "debug"
        - "internal.secret"
```

Values in `set_fields` and `add_fields` support `$variable` substitution. Intermediate objects are created automatically when setting nested paths.

### Field Filtering

Use `allow_fields` or `deny_fields` to include or exclude fields (mutually exclusive):

```yaml
# Allow only specific fields in the response
transform:
  response:
    body:
      allow_fields:
        - "name"
        - "email"
        - "id"
```

```yaml
# Remove sensitive fields from the response
transform:
  response:
    body:
      deny_fields:
        - "password_hash"
        - "internal_id"
        - "internal.secret"
```

### Template Rendering

Use `template` for complete body reshaping with Go `text/template` syntax:

```yaml
transform:
  response:
    body:
      template: |
        {
          "data": {{.body | json}},
          "meta": {
            "request_id": "{{.vars.request_id}}",
            "timestamp": "{{.vars.time_unix}}"
          }
        }
```

Template data:
- `.body` — the parsed JSON body (as a Go interface{})
- `.vars` — a map of gateway variables (`request_id`, `route_id`, `request_method`, `request_path`, `host`, `time_unix`, `time_iso8601`, `remote_addr`)

The `json` template function marshals a value to JSON. Template output must be valid JSON.

When `template` is set, it is the **terminal** operation — the template output replaces the body entirely. Other operations (set/add/remove/rename/allow/deny) are applied before the template.

### Processing Order

Body transform operations are applied in this order:

1. **allow_fields / deny_fields** — filter fields first
2. **set_fields** — set values at dot-notation paths
3. **add_fields** — set values at top-level keys
4. **remove_fields** — delete fields at dot-notation paths
5. **rename_fields** — rename fields (old key → new key)
6. **template** — render Go template (terminal)

## Variables

Header and body values support `$variable` substitution. Available variables:

### Request Variables

| Variable | Description |
|----------|-------------|
| `$request_id` | Auto-generated or propagated request ID |
| `$request_method` | HTTP method (GET, POST, etc.) |
| `$request_uri` | Full request URI |
| `$request_path` | URL path component |
| `$query_string` | Raw query string |
| `$remote_addr` | Client IP address |
| `$remote_port` | Client port |
| `$server_addr` | Server hostname |
| `$server_port` | Server port |
| `$scheme` | `http` or `https` |
| `$host` | Request Host header |
| `$content_type` | Content-Type header |
| `$content_length` | Content-Length value |

### Upstream Variables

| Variable | Description |
|----------|-------------|
| `$upstream_addr` | Backend address used |
| `$upstream_status` | Backend response status code |
| `$upstream_response_time` | Backend response time (ms) |

### Response Variables

| Variable | Description |
|----------|-------------|
| `$status` | Response status code |
| `$body_bytes_sent` | Response body size |
| `$response_time` | Total response time (ms) |

### Time Variables

| Variable | Description |
|----------|-------------|
| `$time_iso8601` | ISO 8601 timestamp |
| `$time_unix` | Unix timestamp (seconds) |
| `$time_local` | Local time format |

### Auth Variables

| Variable | Description |
|----------|-------------|
| `$auth_client_id` | Authenticated client ID |
| `$auth_type` | Auth method used (jwt, api_key) |
| `$route_id` | Current route ID |

### Client Certificate Variables

| Variable | Description |
|----------|-------------|
| `$client_cert_subject` | Certificate subject DN |
| `$client_cert_issuer` | Certificate issuer DN |
| `$client_cert_fingerprint` | SHA-256 fingerprint |
| `$client_cert_serial` | Serial number |
| `$client_cert_dns_names` | DNS SANs (comma-separated) |

### Dynamic Variables

| Pattern | Description |
|---------|-------------|
| `$http_<name>` | Request header value (e.g., `$http_user_agent`) |
| `$arg_<name>` | Query parameter value |
| `$cookie_<name>` | Cookie value |
| `$route_param_<name>` | Path parameter value |
| `$jwt_claim_<name>` | JWT claim value |

## Path Rewriting

Strip the matched path prefix before forwarding to the backend:

```yaml
routes:
  - id: "api"
    path: "/api/v1"
    path_prefix: true
    strip_prefix: true     # /api/v1/users -> /users
    backends:
      - url: "http://backend:9000"
```

## Request Validation

Validate request bodies against a JSON schema:

```yaml
routes:
  - id: "api"
    path: "/api/users"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    validation:
      enabled: true
      # Inline schema:
      schema: '{"type":"object","required":["name","email"],"properties":{"name":{"type":"string"},"email":{"type":"string","format":"email"}}}'
      # Or external file:
      # schema_file: "/etc/runway/schemas/user.json"
```

Invalid requests receive `400 Bad Request` with validation error details.

## Request Decompression

The gateway can automatically decompress incoming request bodies that use `Content-Encoding`. This ensures that downstream middleware (validation, body transforms, WAF, backend signing) and backends receive uncompressed payloads.

Supported algorithms: **gzip**, **deflate**, **Brotli** (`br`), and **Zstd**.

```yaml
# Global — decompress all routes
request_decompression:
  enabled: true
  algorithms: ["gzip", "deflate", "br", "zstd"]  # default: all four
  max_decompressed_size: 52428800                  # 50MB zip bomb protection

# Per-route override
routes:
  - id: "upload"
    path: "/upload"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    request_decompression:
      enabled: true
      max_decompressed_size: 104857600  # 100MB for this route
```

When a request has a `Content-Encoding` header matching a configured algorithm, the gateway:

1. Wraps the request body with the appropriate decompressor
2. Removes the `Content-Encoding` header (body is now uncompressed)
3. Removes the `Content-Length` header (decompressed size is unknown upfront)
4. Enforces `max_decompressed_size` to prevent zip bomb attacks

If decompression fails (corrupt data, unsupported encoding), the gateway returns `400 Bad Request`.

Per-algorithm metrics (request count, decompressed count, errors) are available via the `/decompression` admin endpoint.

## Response Compression

The gateway supports three compression algorithms: **Brotli** (`br`), **Zstd**, and **gzip**. The best algorithm is selected via RFC 7231 `Accept-Encoding` negotiation with quality factor parsing.

Server preference order when quality factors tie: `br` > `zstd` > `gzip`.

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    compression:
      enabled: true
      algorithms: ["br", "zstd", "gzip"]  # default: all three
      level: 6                # 0-11 (default 6; gzip clamped to 9)
      min_size: 1024          # minimum body size to compress (bytes)
      content_types:          # MIME types to compress
        - "application/json"
        - "text/html"
        - "text/plain"
```

The `level` field maps to each algorithm's native range: gzip uses 1-9 (values above 9 are clamped), brotli uses 0-11, and zstd maps via `EncoderLevelFromZstd()`.

Compression only applies when the client sends an `Accept-Encoding` header matching a configured algorithm. Per-algorithm metrics (bytes in/out, count) are available via the `/compression` admin endpoint.

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `transform.request.headers.add` | map | Headers to append |
| `transform.request.headers.set` | map | Headers to replace |
| `transform.request.headers.remove` | []string | Headers to delete |
| `transform.request.body.add_fields` | map | JSON fields to add (top-level) |
| `transform.request.body.set_fields` | map | JSON fields to set (dot-path) |
| `transform.request.body.remove_fields` | []string | JSON fields to remove (dot-path) |
| `transform.request.body.rename_fields` | map | JSON fields to rename |
| `transform.request.body.allow_fields` | []string | Allowlist filter |
| `transform.request.body.deny_fields` | []string | Denylist filter |
| `transform.request.body.template` | string | Go text/template |
| `strip_prefix` | bool | Strip matched path prefix |
| `validation.schema` | string | Inline JSON schema |
| `validation.schema_file` | string | Path to schema file |
| `request_decompression.enabled` | bool | Enable request body decompression |
| `request_decompression.algorithms` | []string | Algorithms: "gzip", "deflate", "br", "zstd" |
| `request_decompression.max_decompressed_size` | int64 | Max decompressed size in bytes (default 50MB) |
| `compression.algorithms` | []string | Algorithms: "gzip", "br", "zstd" |
| `compression.level` | int | Compression level 0-11 |

See [Configuration Reference](../reference/configuration-reference.md#routes) for all fields.
