# CSRF Protection

The gateway provides stateless CSRF (Cross-Site Request Forgery) protection using the double-submit cookie pattern with HMAC-signed tokens, plus optional Origin/Referer validation for defense in depth.

## How It Works

1. **Safe methods** (GET, HEAD, OPTIONS, TRACE by default): The gateway sets a `_csrf` cookie containing an HMAC-signed, timestamped token.
2. **State-changing methods** (POST, PUT, DELETE, PATCH): The client must send the token value in both the cookie and a request header (`X-CSRF-Token`). The gateway verifies that:
   - Both cookie and header are present and match
   - The token has a valid HMAC signature (proving it was issued by the gateway)
   - The token has not expired (based on `token_ttl`)
3. **Origin validation** (optional): If `allowed_origins` or `allowed_origin_patterns` are configured, the `Origin` header (or `Referer` fallback) must match.

This is a **stateless** mechanism — no server-side session storage is needed. The HMAC secret is the shared key that proves token authenticity.

## Configuration

### Global

```yaml
csrf:
  enabled: true
  secret: "${CSRF_SECRET}"        # Required. HMAC signing key.
  cookie_name: "_csrf"            # Default: "_csrf"
  header_name: "X-CSRF-Token"    # Default: "X-CSRF-Token"
  token_ttl: 1h                   # Default: 1h
  cookie_secure: true             # Default: set explicitly in YAML
  cookie_samesite: "lax"          # "strict", "lax", or "none". Default: "lax"
  cookie_http_only: false         # Must be false so JS can read the cookie
  inject_token: true              # Set token cookie on safe methods. Default: set explicitly
  allowed_origins:                # Optional: exact origin allow list
    - "https://app.example.com"
  allowed_origin_patterns:        # Optional: regex patterns
    - "^https://.*\\.example\\.com$"
  shadow_mode: false              # Log failures without rejecting
  exempt_paths:                   # Glob patterns that skip CSRF checks
    - "/api/webhooks"
    - "/health"
```

### Per-Route

```yaml
routes:
  - id: "web-app"
    path: "/app"
    path_prefix: true
    backends:
      - url: "http://backend:8080"
    csrf:
      enabled: true
      secret: "${CSRF_SECRET}"
      cookie_secure: true
      inject_token: true
      allowed_origins:
        - "https://app.example.com"
```

Per-route config is merged with the global `csrf:` block. Per-route non-zero fields override global values.

## Token Lifecycle

1. Client sends `GET /page` to the gateway
2. Gateway sets `Set-Cookie: _csrf=<token>; Path=/; SameSite=Lax; Secure`
3. Client-side JavaScript reads the cookie and includes it in subsequent requests:
   ```javascript
   const token = document.cookie.match(/(?:^|;\s*)_csrf=([^;]*)/)?.[1];
   fetch('/api/submit', {
     method: 'POST',
     headers: {
       'X-CSRF-Token': token,
       'Content-Type': 'application/json'
     },
     credentials: 'same-origin',
     body: JSON.stringify(data)
   });
   ```
4. Gateway validates: cookie == header, valid HMAC, not expired

## Origin Validation

When `allowed_origins` or `allowed_origin_patterns` are configured, the gateway checks the `Origin` header on state-changing requests. If `Origin` is absent, it falls back to the `Referer` header's scheme+host. If neither is present, the request is rejected.

This provides defense in depth — even if an attacker manages to obtain a valid token, the origin check prevents cross-site submission.

## Shadow Mode

Set `shadow_mode: true` to log CSRF failures without rejecting requests. This is useful for gradual rollout:

1. Deploy with `shadow_mode: true`
2. Monitor logs for `CSRF token missing (shadow mode)` and similar warnings
3. Once clients are updated to send tokens, disable shadow mode

## Safe Methods

By default, GET, HEAD, OPTIONS, and TRACE are considered safe (no token validation). Override with:

```yaml
csrf:
  safe_methods: ["GET", "HEAD", "OPTIONS"]
```

## Exempt Paths

Paths matching `exempt_paths` glob patterns skip CSRF validation entirely. Use for webhook endpoints or health checks:

```yaml
csrf:
  exempt_paths:
    - "/api/webhooks*"
    - "/health"
```

## Cookie Attributes

| Field | Default | Description |
|-------|---------|-------------|
| `cookie_name` | `_csrf` | Cookie name |
| `cookie_path` | `/` | Cookie path |
| `cookie_domain` | (empty) | Cookie domain |
| `cookie_secure` | Set in YAML | Secure flag (required for SameSite=None) |
| `cookie_samesite` | `lax` | SameSite attribute: strict, lax, none |
| `cookie_http_only` | `false` | HttpOnly flag (must be false for JS access) |

## Validation Rules

- `secret` is required when `enabled: true`
- `cookie_samesite: "none"` requires `cookie_secure: true`
- `token_ttl` must be >= 0
- `allowed_origin_patterns` must be valid regular expressions

## Admin API

### GET `/csrf`

Returns per-route CSRF protection status and metrics.

```bash
curl http://localhost:8081/csrf
```

**Response:**
```json
{
  "web-app": {
    "cookie_name": "_csrf",
    "header_name": "X-CSRF-Token",
    "token_ttl": "1h0m0s",
    "shadow_mode": false,
    "inject_token": true,
    "total_requests": 5000,
    "token_generated": 2000,
    "validation_success": 2900,
    "validation_failed": 100,
    "origin_check_failed": 5,
    "missing_token": 80,
    "expired_token": 10,
    "invalid_signature": 5
  }
}
```

## Security Considerations

- Use a strong, random `secret` (at least 32 bytes). Use environment variable substitution: `secret: "${CSRF_SECRET}"`.
- Set `cookie_secure: true` in production to prevent cookie transmission over HTTP.
- Set `cookie_samesite: "lax"` (default) or `"strict"` to prevent most cross-site cookie sending.
- Keep `cookie_http_only: false` so client-side JavaScript can read the cookie value and send it as a header. The HMAC signature prevents forgery even if the cookie is readable.
- The token's HMAC proves it was issued by the gateway. An attacker cannot forge tokens without the secret.
- Origin validation provides an additional layer — even with a stolen token, the browser will send the real origin.
