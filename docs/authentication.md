# Authentication

The gateway supports multiple authentication methods that can be combined per route. Authentication is configured globally (provider settings) and per route (which methods are required).

## API Key Authentication

Validates requests against a list of known API keys, checked in a header or query parameter.

```yaml
authentication:
  api_key:
    enabled: true
    header: "X-API-Key"         # or use query_param: "api_key"
    keys:
      - key: "${API_KEY_1}"
        client_id: "client-1"
        name: "Production Client"
        expires_at: "2026-12-31T23:59:59Z"  # optional RFC3339 expiry
        roles: ["admin", "read"]            # optional roles
      - key: "${API_KEY_2}"
        client_id: "client-2"
        name: "Staging Client"
```

API keys can be managed at runtime via the [Admin API](admin-api.md) at `/admin/keys` (GET, POST, DELETE).

### API Key Management

The gateway provides built-in API key lifecycle management: generation, rotation (with grace periods), revocation, and per-key rate limits. This eliminates the need for an external key management service.

```yaml
authentication:
  api_key:
    enabled: true
    header: "X-API-Key"
    keys: []  # static keys still supported alongside managed keys
    management:
      enabled: true
      key_length: 32          # bytes (default 32, range 16-128)
      key_prefix: "gw_"      # prefix for generated keys
      store: memory           # "memory" or "redis"
      default_rate_limit:     # optional default per-key rate limit
        rate: 100
        period: 1m
        burst: 20
```

**Key operations** (via Admin API):

- **Generate:** `POST /api-keys/generate` — returns the raw key once (only time visible)
- **Rotate:** `POST /api-keys/{prefix}/rotate` — creates a new key with a grace period for the old key
- **Revoke:** `POST /api-keys/{prefix}/revoke` — marks key as revoked (returns 403, not 401)
- **Unrevoke:** `POST /api-keys/{prefix}/unrevoke` — restores a revoked key
- **Delete:** `DELETE /api-keys/{prefix}/delete` — permanently removes the key
- **Stats:** `GET /api-keys/stats` — management statistics

Managed keys are looked up by SHA-256 hash — the raw key is never stored. When both managed and static keys are configured, managed keys are checked first.

## JWT Authentication

Validates JSON Web Tokens using HMAC shared secrets, RSA public keys, or remote JWKS endpoints.

```yaml
authentication:
  jwt:
    enabled: true
    algorithm: "RS256"
    # Static key:
    # secret: "${JWT_SECRET}"       # for HS256
    # public_key: "${JWT_PUB_KEY}"  # for RS256
    # Or dynamic JWKS:
    jwks_url: "https://auth.example.com/.well-known/jwks.json"
    jwks_refresh_interval: 1h
    issuer: "https://auth.example.com"
    audience: ["my-api"]
```

JWT claims are accessible as variables (`$jwt_claim_sub`, `$jwt_claim_role`) and in the rules engine (`auth.claims["sub"]`).

### JWKS Auto-Refresh

When `jwks_url` is set, the gateway fetches and caches the JSON Web Key Set, automatically refreshing it on the configured interval. This supports key rotation without gateway restarts.

## OAuth 2.0 / OIDC

Validates bearer tokens via token introspection or JWKS, with scope enforcement.

```yaml
authentication:
  oauth:
    enabled: true
    introspection_url: "https://auth.example.com/oauth/introspect"
    client_id: "${OAUTH_CLIENT_ID}"
    client_secret: "${OAUTH_CLIENT_SECRET}"
    # Or JWKS-based:
    # jwks_url: "https://auth.example.com/.well-known/jwks.json"
    issuer: "https://auth.example.com"
    audience: "my-api"
    scopes: ["read", "write"]
    cache_ttl: 5m
```

## External Auth (ExtAuth)

Delegates authentication decisions to an external HTTP or gRPC service. This is the "ForwardAuth" / "ext_authz" pattern used by Traefik, Envoy, and NGINX. The gateway sends request metadata to the external service and acts on its allow/deny response.

### HTTP Mode

```yaml
routes:
  - id: "protected"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    ext_auth:
      enabled: true
      url: "http://auth-service:8080/authorize"
      timeout: 3s
      fail_open: false
      headers_to_send:
        - "Authorization"
        - "X-Forwarded-For"
        - "Cookie"
      headers_to_inject:
        - "X-Auth-User"
        - "X-Auth-Roles"
      cache_ttl: 30s
```

The gateway sends a POST request to the auth URL with a JSON body containing the request method, path, and selected headers. A `200` response means allow; any other status means deny, and the status code and body are returned to the client.

### gRPC Mode

```yaml
    ext_auth:
      enabled: true
      url: "grpc://auth-service:50051"
      timeout: 3s
      tls:
        enabled: true
        ca_file: "/certs/ca.pem"
```

The gateway invokes `/extauth.AuthService/Check` using a JSON codec. The request and response are JSON-encoded `CheckRequest`/`CheckResponse` structs.

### Header Injection

On allow, the auth service can return response headers that get injected into the upstream request. Use `headers_to_inject` to limit which headers are copied (empty = all headers from the auth response).

### Fail-Open vs Fail-Closed

- **`fail_open: false`** (default): If the auth service is unreachable or returns an error, the request is denied with `502 Bad Gateway`.
- **`fail_open: true`**: If the auth service is unreachable, the request is allowed to proceed.

### Caching

Set `cache_ttl` to cache successful auth results. The cache key is computed from the request method, path, and selected headers. Only allow results are cached; deny results are never cached.

### Combining with Built-in Auth

ExtAuth runs **after** built-in auth (JWT, API Key, OAuth), so identity information from built-in providers is available to the ext auth service via forwarded headers. Both can be used on the same route.

### Key Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ext_auth.enabled` | bool | false | Enable external auth |
| `ext_auth.url` | string | | Auth service URL (`http://`, `https://`, or `grpc://`) |
| `ext_auth.timeout` | duration | 5s | Request timeout |
| `ext_auth.fail_open` | bool | false | Allow on auth service error |
| `ext_auth.headers_to_send` | []string | all | Request headers to forward |
| `ext_auth.headers_to_inject` | []string | all | Auth response headers to inject upstream |
| `ext_auth.cache_ttl` | duration | 0 | Cache TTL (0 = disabled) |
| `ext_auth.tls.enabled` | bool | false | Enable TLS |
| `ext_auth.tls.ca_file` | string | | CA certificate file |
| `ext_auth.tls.cert_file` | string | | Client certificate (mTLS) |
| `ext_auth.tls.key_file` | string | | Client key (mTLS) |

## mTLS (Client Certificates)

When mTLS is enabled on a listener, client certificate fields are extracted and made available as variables and in the rules engine:

| Variable | Description |
|----------|-------------|
| `$client_cert_subject` | Certificate subject DN |
| `$client_cert_issuer` | Certificate issuer DN |
| `$client_cert_fingerprint` | SHA-256 fingerprint |
| `$client_cert_serial` | Serial number |
| `$client_cert_dns_names` | Comma-separated DNS SANs |

See [Core Concepts — mTLS](core-concepts.md#mtls-mutual-tls) for listener TLS configuration.

## Per-Route Authentication

Each route specifies whether auth is required and which methods to use:

```yaml
routes:
  - id: "protected-api"
    path: "/api/protected"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    auth:
      required: true
      methods: ["jwt", "api_key"]   # accepts either method

  - id: "public-api"
    path: "/api/public"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    auth:
      required: false               # no authentication needed
```

When `required: true` and no valid credential is provided, the gateway returns `401 Unauthorized`.

## Claims Propagation

Forward JWT claims as request headers to backend services. Configured per-route; runs after authentication succeeds.

```yaml
routes:
  - id: my-route
    path: /api/v1/*
    auth:
      required: true
      methods: [jwt]
    claims_propagation:
      enabled: true
      claims:
        sub: "X-User-ID"
        email: "X-User-Email"
        org_id: "X-Org-ID"
        user.role: "X-User-Role"     # dot notation for nested claims
```

Each entry maps a JWT claim name to a request header name. The gateway reads claims from the authenticated identity (populated by the auth middleware) and sets the corresponding headers before forwarding to the backend.

- **Dot notation**: `user.role` extracts `claims["user"]["role"]` from nested claim objects
- **Non-string values**: Automatically converted via `fmt.Sprintf("%v", val)`
- **Missing claims**: Silently skipped (no error, no header set)
- **No identity**: If the request has no authenticated identity, propagation is skipped entirely

Admin endpoint: `GET /claims-propagation` returns per-route propagation statistics.

## Token Revocation

Blocklist revoked JWT tokens by JTI or token hash. Supports both in-memory and distributed (Redis) storage.

```yaml
token_revocation:
  enabled: true
  mode: local              # "local" (default) or "distributed"
  default_ttl: 24h         # max time to keep revoked tokens (default 24h)
```

### How It Works

1. After authentication, the gateway extracts the Bearer token from the `Authorization` header
2. If the token has a `jti` claim, that value is used as the revocation key
3. If no `jti`, the SHA256 hash of the full token is used (first 32 hex chars)
4. The key is checked against the revocation store — if found, the request is rejected with `401 Unauthorized`

Token revocation only applies to routes where `auth.required: true`.

### Revoking Tokens via Admin API

```bash
# Revoke by full JWT token (extracts JTI automatically)
curl -X POST http://localhost:8081/token-revocation/revoke \
  -d '{"token":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyLTEiLCJqdGkiOiJhYmMtMTIzIn0.sig"}'

# Revoke by JTI directly (with custom TTL)
curl -X POST http://localhost:8081/token-revocation/revoke \
  -d '{"jti":"abc-123","ttl":"2h"}'

# Unrevoke a token
curl -X POST http://localhost:8081/token-revocation/unrevoke \
  -d '{"jti":"abc-123"}'

# Check revocation stats
curl http://localhost:8081/token-revocation
```

### TTL Behavior

- If the token has an `exp` claim, the revocation TTL is capped at `exp - now` (no point storing a revocation longer than the token lives)
- Explicit TTL in the revoke request is also capped at `default_ttl`
- If no TTL is specified, `default_ttl` is used

### Distributed Mode

With `mode: distributed`, the revocation store uses Redis (requires `redis.address` in config). This ensures revocations are shared across gateway instances. Redis keys are prefixed with `gw:revoked:` and use TTL-based expiration. The store fails open on Redis errors (allows the request through).

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `authentication.api_key.enabled` | bool | Enable API key auth |
| `authentication.api_key.header` | string | Header name to check (default `X-API-Key`) |
| `authentication.jwt.algorithm` | string | `HS256`, `RS256`, etc. |
| `authentication.jwt.jwks_url` | string | JWKS endpoint for dynamic key fetching |
| `authentication.oauth.scopes` | []string | Required OAuth scopes |
| `auth.required` | bool | Require auth on this route |
| `auth.methods` | []string | Allowed methods: `jwt`, `api_key`, `oauth` |
| `claims_propagation.enabled` | bool | Enable claims propagation (per-route) |
| `claims_propagation.claims` | map | Claim-to-header mappings |
| `token_revocation.enabled` | bool | Enable token revocation (global) |
| `token_revocation.mode` | string | `local` or `distributed` |
| `token_revocation.default_ttl` | duration | Max revocation TTL (default 24h) |

| `backend_auth.enabled` | bool | Enable backend auth token injection (per-route) |
| `backend_auth.type` | string | `oauth2_client_credentials` (required) |
| `backend_auth.token_url` | string | Token endpoint URL |

See [Configuration Reference](configuration-reference.md#authentication) for all fields.

## Backend Auth (OAuth2 Client Credentials)

The gateway can act as an OAuth2 client, fetching access tokens from an identity server using the `client_credentials` grant and injecting them as `Authorization: Bearer <token>` headers into backend requests.

Tokens are cached in memory and auto-refreshed 10 seconds before expiry. If a token refresh fails, the request proceeds without an Authorization header (logged as a warning).

```yaml
routes:
  - id: protected-api
    path: /api/
    path_prefix: true
    backends:
      - url: http://backend:8080
    backend_auth:
      enabled: true
      type: oauth2_client_credentials
      token_url: https://auth.example.com/oauth/token
      client_id: "${GATEWAY_CLIENT_ID}"
      client_secret: "${GATEWAY_CLIENT_SECRET}"
      scopes:
        - read
        - write
      extra_params:
        audience: https://api.example.com
      timeout: 5s
```

The middleware is positioned at step 16.25 in the chain — after request transforms and before backend signing. This ensures the `Authorization` header is included in HMAC signature computation when backend signing is also enabled.

**Admin endpoint:** `GET /backend-auth` returns per-route token refresh stats.

---

## Token Exchange (RFC 8693)

The gateway can act as a Security Token Service (STS) intermediary, accepting external IdP tokens and issuing internal service tokens. This enables zero-trust architectures where backends only trust gateway-issued tokens.

### How It Works

1. Client sends a request with `Authorization: Bearer <external-token>`
2. Gateway validates the external token (JWT via JWKS, or introspection)
3. Gateway mints a new internal JWT with mapped claims
4. Backend receives the gateway-issued token in the `Authorization` header

### Configuration

```yaml
routes:
  - id: partner-api
    path: /api/partner
    auth:
      required: true
      methods: [jwt]
    token_exchange:
      enabled: true
      validation_mode: jwt          # "jwt" or "introspection"
      jwks_url: https://partner-idp.example.com/.well-known/jwks.json
      trusted_issuers:
        - https://partner-idp.example.com
      issuer: https://gateway.internal.example.com
      audience: [internal-services]
      scopes: [read, write]
      token_lifetime: 15m
      signing_algorithm: RS256      # RS256, RS512, HS256, HS512
      signing_key_file: /etc/gateway/exchange-key.pem
      cache_ttl: 14m
      claim_mappings:
        sub: sub
        email: email
        groups: roles
```

### Validation Modes

- **jwt:** Validates tokens locally using JWKS. Requires `jwks_url` and `trusted_issuers`.
- **introspection:** Validates via OAuth2 introspection endpoint. Requires `introspection_url`, `client_id`, `client_secret`.

### Claim Mappings

The `claim_mappings` field maps claims from the subject token to the issued token. The key is the claim name in the subject token, the value is the claim name in the issued token.

### Caching

Exchange results are cached by SHA-256 of the subject token. Set `cache_ttl` slightly less than `token_lifetime` to avoid serving expired tokens from cache.

The middleware is positioned at step 6.07 in the chain — after auth (6) and token revocation (6.05), before claims propagation (6.15).

**Admin endpoint:** `GET /token-exchange` returns per-route exchange metrics.
