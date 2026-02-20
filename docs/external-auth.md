# External Authentication

External authentication delegates authorization decisions to an external HTTP or gRPC service. The gateway sends a check request containing the incoming request's method, path, and headers. The external service responds with allow or deny, optionally injecting headers into the upstream request.

## Configuration

```yaml
routes:
  - id: protected-api
    path: /api/
    path_prefix: true
    backends:
      - url: http://backend:8080
    ext_auth:
      enabled: true
      url: http://auth-service:9090/check
      timeout: 3s
      fail_open: false
      headers_to_send:
        - Authorization
        - X-Forwarded-For
        - Cookie
      headers_to_inject:
        - X-User-ID
        - X-User-Role
      cache_ttl: 30s
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable external auth for this route |
| `url` | string | required | Auth service URL (`http://`, `https://`, or `grpc://`) |
| `timeout` | duration | 5s | Request timeout for auth checks |
| `fail_open` | bool | false | Allow requests when the auth service is unreachable |
| `headers_to_send` | list | all | Request headers to forward to the auth service (empty = send all) |
| `headers_to_inject` | list | all | Auth response headers to copy to the upstream request (empty = inject all) |
| `cache_ttl` | duration | 0 (disabled) | Cache duration for successful allow results |
| `tls.enabled` | bool | false | Enable TLS for the auth service connection |
| `tls.ca_file` | string | - | CA certificate file for server verification |
| `tls.cert_file` | string | - | Client certificate file (for mTLS) |
| `tls.key_file` | string | - | Client key file (for mTLS) |

## HTTP Protocol

When the `url` starts with `http://` or `https://`, the gateway sends a POST request with a JSON body:

```json
{
  "method": "GET",
  "path": "/api/users",
  "headers": {
    "Authorization": "Bearer eyJ...",
    "X-Forwarded-For": "10.0.0.1"
  }
}
```

The selected `headers_to_send` are also set as HTTP headers on the check request itself.

**Allow (200 OK):** The request proceeds to the backend. Response headers from the auth service matching `headers_to_inject` are added to the upstream request.

**Deny (any non-200):** The auth service's status code, body, and headers are returned directly to the client. `Content-Length` and `Transfer-Encoding` headers are stripped. If the auth service returns no status code, `403 Forbidden` is used.

## gRPC Protocol

When the `url` starts with `grpc://`, the gateway calls `extauth.AuthService/Check` using JSON encoding. The request body is the same `CheckRequest` structure as HTTP.

The auth service responds with a `CheckResponse`:

```json
{
  "allowed": true,
  "headers": {
    "X-User-ID": "user-123"
  }
}
```

On deny:

```json
{
  "allowed": false,
  "denied_status": 403,
  "denied_body": "{\"error\": \"forbidden\"}",
  "denied_headers": {
    "X-Error-Code": "AUTH_FAILED"
  }
}
```

## TLS and mTLS

For HTTPS or gRPC with TLS:

```yaml
ext_auth:
  enabled: true
  url: https://auth-service:9443/check
  tls:
    enabled: true
    ca_file: /etc/certs/ca.pem
    cert_file: /etc/certs/client.pem
    key_file: /etc/certs/client-key.pem
```

- `ca_file` configures a custom CA for server verification
- `cert_file` + `key_file` enables mTLS client authentication
- TLS cannot be used with `http://` URLs (validation error)

## Caching

When `cache_ttl` is set, successful allow results are cached in memory. The cache key is derived from the request method, path, and the headers selected by `headers_to_send`. Only allow results are cached; denials are never cached.

```yaml
ext_auth:
  enabled: true
  url: http://auth-service:9090/check
  cache_ttl: 60s
```

## Fail-Open vs Fail-Closed

| Mode | Config | Behavior on auth service error |
|------|--------|-------------------------------|
| Fail-closed | `fail_open: false` (default) | Request is rejected with 502 Bad Gateway |
| Fail-open | `fail_open: true` | Request is allowed through to the backend |

Fail-open mode is useful for non-critical auth checks where availability is prioritized over strict enforcement. Errors are always counted in metrics regardless of mode.

## Middleware Position

Step 6.25 in the middleware chain — after auth (6), token revocation (6.05), and claims propagation (6.15); before nonce replay prevention (6.3). This ordering ensures that JWT authentication and claims extraction happen before the external auth check, making identity information available to the auth service via forwarded headers.

## Admin API

### GET `/ext-auth`

Returns external auth metrics including request counts, cache hit rates, and latency percentiles.

```bash
curl http://localhost:8081/ext-auth
```

**Response:**
```json
{
  "protected-api": {
    "total": 15000,
    "allowed": 14200,
    "denied": 750,
    "errors": 50,
    "cache_hits": 8400,
    "latency_p50_ms": 2000000,
    "latency_p95_ms": 8000000,
    "latency_p99_ms": 15000000
  }
}
```

Latency values are in nanoseconds (Go `time.Duration`).

## Validation Rules

- `url` is required when `enabled: true`
- `url` must start with `http://`, `https://`, or `grpc://`
- `timeout` must be >= 0
- `cache_ttl` must be >= 0
- `tls.enabled: true` cannot be used with `http://` URLs
