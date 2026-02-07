# Transformations

The gateway can modify requests and responses as they pass through the proxy. Transformations include header manipulation, body modification, variable substitution, path rewriting, request validation, and response compression.

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

Modify JSON request or response bodies by adding, removing, or renaming fields:

```yaml
transform:
  request:
    body:
      add_fields:
        source: "gateway"
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

Body transforms only apply to `application/json` content.

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
      # schema_file: "/etc/gateway/schemas/user.json"
```

Invalid requests receive `400 Bad Request` with validation error details.

## Response Compression

Compress responses with gzip:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    compression:
      enabled: true
      level: 6                # 1-9 (default 6)
      min_size: 1024          # minimum body size to compress (bytes)
      content_types:          # MIME types to compress
        - "application/json"
        - "text/html"
        - "text/plain"
```

Compression only applies when the client sends `Accept-Encoding: gzip`.

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `transform.request.headers.add` | map | Headers to append |
| `transform.request.headers.set` | map | Headers to replace |
| `transform.request.headers.remove` | []string | Headers to delete |
| `transform.request.body.add_fields` | map | JSON fields to add |
| `strip_prefix` | bool | Strip matched path prefix |
| `validation.schema` | string | Inline JSON schema |
| `validation.schema_file` | string | Path to schema file |
| `compression.level` | int | Gzip level 1-9 |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
