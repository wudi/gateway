---
title: "Authentication"
sidebar_position: 2
---

The runway supports multiple authentication methods that can be combined per route. Authentication is configured globally (provider settings) and per route (which methods are required).

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

API keys can be managed at runtime via the [Admin API](../reference/admin-api.md) at `/admin/keys` (GET, POST, DELETE).

### API Key Management

The runway provides built-in API key lifecycle management: generation, rotation (with grace periods), revocation, and per-key rate limits. This eliminates the need for an external key management service.

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

When `jwks_url` is set, the runway fetches and caches the JSON Web Key Set, automatically refreshing it on the configured interval. This supports key rotation without runway restarts.

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

## Basic Authentication

Validates requests using HTTP Basic Authentication against a local list of users with bcrypt-hashed passwords. This is the simplest auth method and is suitable for internal tools, staging environments, or small-scale APIs.

```yaml
authentication:
  basic:
    enabled: true
    realm: "My API"    # optional, default "Restricted"
    users:
      - username: "admin"
        password_hash: "$2a$10$..."    # bcrypt hash
        client_id: "admin-user"
        roles: ["admin", "write"]
      - username: "reader"
        password_hash: "$2a$10$..."
        client_id: "reader-user"
        roles: ["read"]
```

### Generating Password Hashes

Use `htpasswd` (from Apache) or any bcrypt tool:

```bash
htpasswd -nbBC 10 admin mypassword | cut -d: -f2
```

Or in Go:

```go
hash, _ := bcrypt.GenerateFromPassword([]byte("mypassword"), bcrypt.DefaultCost)
```

### Timing-Safe Authentication

When an unknown username is submitted, the runway still runs a bcrypt comparison against a dummy hash. This prevents timing-based user enumeration attacks.

### Per-Route Usage

Basic auth is **not** included in the default auth methods — it must be explicitly listed in `auth.methods` to avoid triggering browser credential dialogs on API routes:

```yaml
routes:
  - id: "internal-tool"
    path: "/internal/*"
    backends:
      - url: "http://backend:9000"
    auth:
      required: true
      methods: ["basic"]
```

## LDAP Authentication

Validates requests using HTTP Basic Authentication against an LDAP or Active Directory server. The runway uses a bind-search-bind flow: it binds as a service account, searches for the user, then binds as the found user to verify the password.

```yaml
authentication:
  ldap:
    enabled: true
    url: "ldap://ldap.example.com:389"      # or ldaps:// for TLS
    start_tls: false
    bind_dn: "cn=gateway,ou=services,dc=example,dc=org"
    bind_password: "${LDAP_BIND_PASSWORD}"   # env var expansion
    user_search_base: "ou=users,dc=example,dc=org"
    user_search_filter: "(uid={{username}})"
    user_search_scope: "sub"                 # sub (default), one, base
    realm: "LDAP"
    attribute_mapping:
      client_id: "uid"                       # default "uid"
      email: "mail"
      display_name: "displayName"
    tls:
      skip_verify: false
      ca_file: "/etc/certs/ldap-ca.pem"
    cache_ttl: 5m           # auth result cache TTL (default 5m)
    conn_timeout: 10s       # connection timeout (default 10s)
    max_conn_lifetime: 5m   # max lifetime for pooled connections (default 5m)
    pool_size: 5            # connection pool size (default 5)
```

### Bind-Search-Bind Flow

1. The runway binds to LDAP as the service account (`bind_dn` / `bind_password`)
2. It searches for the user using the `user_search_filter` with `{{username}}` replaced by the login name (LDAP-escaped)
3. If found, it binds as the user's DN with the provided password to verify credentials
4. If `group_search_base` is set, it re-binds as the service account and searches for group membership

### Active Directory Example

```yaml
authentication:
  ldap:
    enabled: true
    url: "ldaps://ad.corp.example.com:636"
    bind_dn: "CN=Gateway Service,OU=Services,DC=corp,DC=example,DC=com"
    bind_password: "${AD_BIND_PASSWORD}"
    user_search_base: "OU=Users,DC=corp,DC=example,DC=com"
    user_search_filter: "(sAMAccountName={{username}})"
    attribute_mapping:
      client_id: "sAMAccountName"
      email: "mail"
      display_name: "displayName"
    group_search_base: "OU=Groups,DC=corp,DC=example,DC=com"
    group_search_filter: "(member={{dn}})"
    group_attribute: "cn"
    tls:
      skip_verify: false
```

### Group Membership

When `group_search_base` is set, the runway searches for groups the user belongs to. The `group_search_filter` supports `{{dn}}` (user's DN) and `{{username}}` placeholders. The `group_attribute` (default `cn`) is extracted from each group entry and included as `roles` in the identity claims.

### Connection Pool & Caching

- **Connection pool:** Maintains a pool of reusable LDAP connections (`pool_size`, default 5). Stale connections older than `max_conn_lifetime` are discarded.
- **Result cache:** Successful authentications are cached using an LRU cache (capacity 10,000) with per-entry TTL (`cache_ttl`, default 5m). Cache entries are evicted individually on TTL expiry — no thundering-herd evict-all.

### Admin Endpoint

`GET /ldap/stats` returns authentication statistics:

```json
{
  "attempts": 1234,
  "successes": 1200,
  "failures": 34,
  "cache_hits": 950,
  "cache_misses": 284,
  "pool_size": 3
}
```

### Per-Route Usage

Like basic auth, LDAP auth must be explicitly listed in `auth.methods`:

```yaml
routes:
  - id: "corp-api"
    path: "/api/*"
    backends:
      - url: "http://backend:9000"
    auth:
      required: true
      methods: ["ldap"]
```

## External Auth (ExtAuth)

Delegates authentication decisions to an external HTTP or gRPC service. This is the "ForwardAuth" / "ext_authz" pattern used by Traefik, Envoy, and NGINX. The runway sends request metadata to the external service and acts on its allow/deny response.

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

The runway sends a POST request to the auth URL with a JSON body containing the request method, path, and selected headers. A `200` response means allow; any other status means deny, and the status code and body are returned to the client.

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

The runway invokes `/extauth.AuthService/Check` using a JSON codec. The request and response are JSON-encoded `CheckRequest`/`CheckResponse` structs.

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

See [Core Concepts — mTLS](../getting-started/core-concepts.md#mtls-mutual-tls) for listener TLS configuration.

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

When `required: true` and no valid credential is provided, the runway returns `401 Unauthorized`.

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

Each entry maps a JWT claim name to a request header name. The runway reads claims from the authenticated identity (populated by the auth middleware) and sets the corresponding headers before forwarding to the backend.

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

1. After authentication, the runway extracts the Bearer token from the `Authorization` header
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

With `mode: distributed`, the revocation store uses Redis (requires `redis.address` in config). This ensures revocations are shared across runway instances. Redis keys are prefixed with `gw:revoked:` and use TTL-based expiration. The store fails open on Redis errors (allows the request through).

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `authentication.api_key.enabled` | bool | Enable API key auth |
| `authentication.api_key.header` | string | Header name to check (default `X-API-Key`) |
| `authentication.jwt.algorithm` | string | `HS256`, `RS256`, etc. |
| `authentication.jwt.jwks_url` | string | JWKS endpoint for dynamic key fetching |
| `authentication.oauth.scopes` | []string | Required OAuth scopes |
| `auth.required` | bool | Require auth on this route |
| `authentication.basic.enabled` | bool | Enable Basic auth |
| `authentication.basic.realm` | string | WWW-Authenticate realm (default `Restricted`) |
| `authentication.ldap.enabled` | bool | Enable LDAP auth |
| `authentication.ldap.url` | string | LDAP server URL (`ldap://` or `ldaps://`) |
| `authentication.ldap.cache_ttl` | duration | Auth result cache TTL (default 5m) |
| `authentication.ldap.pool_size` | int | Connection pool size (default 5) |
| `auth.methods` | []string | Allowed methods: `jwt`, `api_key`, `oauth`, `basic`, `ldap` |
| `claims_propagation.enabled` | bool | Enable claims propagation (per-route) |
| `claims_propagation.claims` | map | Claim-to-header mappings |
| `token_revocation.enabled` | bool | Enable token revocation (global) |
| `token_revocation.mode` | string | `local` or `distributed` |
| `token_revocation.default_ttl` | duration | Max revocation TTL (default 24h) |

| `backend_auth.enabled` | bool | Enable backend auth token injection (per-route) |
| `backend_auth.type` | string | `oauth2_client_credentials` (required) |
| `backend_auth.token_url` | string | Token endpoint URL |

See [Configuration Reference](../reference/configuration-reference.md#authentication) for all fields.

## Backend Auth (OAuth2 Client Credentials)

The runway can act as an OAuth2 client, fetching access tokens from an identity server using the `client_credentials` grant and injecting them as `Authorization: Bearer <token>` headers into backend requests.

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

The runway can act as a Security Token Service (STS) intermediary, accepting external IdP tokens and issuing internal service tokens. This enables zero-trust architectures where backends only trust runway-issued tokens.

### How It Works

1. Client sends a request with `Authorization: Bearer <external-token>`
2. Runway validates the external token (JWT via JWKS, or introspection)
3. Runway mints a new internal JWT with mapped claims
4. Backend receives the runway-issued token in the `Authorization` header

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
      issuer: https://runway.internal.example.com
      audience: [internal-services]
      scopes: [read, write]
      token_lifetime: 15m
      signing_algorithm: RS256      # RS256, RS512, HS256, HS512
      signing_key_file: /etc/runway/exchange-key.pem
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

---

## SAML 2.0 SSO

The runway can act as a SAML 2.0 Service Provider (SP), enabling browser-based Single Sign-On with enterprise identity providers (Okta, Azure AD, ADFS, OneLogin, etc.). It also supports stateless token validation via SAML assertions passed in an HTTP header.

### Authentication Modes

**Browser SSO (SP-initiated):** The user visits a protected route, gets redirected to the IdP login page, authenticates, and is redirected back to the runway's ACS endpoint. The runway validates the SAML response, creates a signed session cookie, and redirects the user to the original URL.

**Header-based token validation:** For API clients, a Base64-encoded SAML assertion can be passed in the `X-SAML-Assertion` header (configurable). The runway validates the assertion's XML structure, time conditions, and checks for replay before granting access.

### SAML Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/saml/metadata` | GET | SP metadata XML for IdP registration |
| `/saml/login` | GET | Initiates SSO redirect to IdP |
| `/saml/acs` | POST | Assertion Consumer Service — processes IdP response |
| `/saml/slo` | GET/POST | Single Logout (SP-initiated and IdP-initiated) |

The path prefix (`/saml/`) is configurable via `path_prefix`.

### Configuration

```yaml
authentication:
  saml:
    enabled: true
    entity_id: "https://runway.example.com"
    cert_file: /etc/runway/saml/sp.cert      # SP X.509 certificate (PEM)
    key_file: /etc/runway/saml/sp.key         # SP private key (PEM)
    idp_metadata_url: https://idp.example.com/metadata   # OR idp_metadata_file
    # idp_metadata_file: /etc/runway/saml/idp-metadata.xml
    metadata_refresh_interval: 24h             # auto-refresh IdP metadata (0 disables)
    path_prefix: /saml/                        # default "/saml/"
    name_id_format: email                      # email, persistent, transient, unspecified
    sign_requests: true                        # sign AuthnRequests (default true)
    force_authn: false                         # force re-authentication at IdP
    allow_idp_initiated: false                 # allow unsolicited IdP responses
    assertion_header: X-SAML-Assertion         # header for stateless mode
    session:
      signing_key: "${SAML_SESSION_KEY}"       # HMAC key for session JWT (>= 32 bytes)
      cookie_name: runway_saml                # default "runway_saml"
      max_age: 8h                              # session lifetime
      domain: .example.com                     # cookie domain
      secure: true                             # Secure flag (default true)
      same_site: lax                           # lax, strict, none
    attribute_mapping:
      client_id: uid                           # SAML attribute → Identity.ClientID
      email: email
      display_name: displayName
      roles: groups                            # multi-valued → []string
```

### Per-Route Usage

SAML is not included in the default auth methods (it requires browser redirects). Add it explicitly:

```yaml
routes:
  - id: dashboard
    path: /dashboard
    path_prefix: true
    auth:
      required: true
      methods: [saml]
    backends:
      - url: http://dashboard-service:8080
```

You can combine SAML with other methods. The runway tries each method in order and uses the first that succeeds:

```yaml
    auth:
      required: true
      methods: [jwt, saml]   # API clients use JWT; browsers use SAML session
```

### Single Logout (SLO)

**SP-initiated:** A GET to `/saml/slo` clears the session cookie and redirects the user to the IdP's SLO endpoint.

**IdP-initiated:** The IdP sends a LogoutRequest to `/saml/slo`. The runway clears the session and responds with a LogoutResponse.

### IdP Setup

1. Start the runway with SAML enabled
2. Access `GET /saml/metadata` to download the SP metadata XML
3. Register the SP in your IdP using this metadata (or manually configure ACS URL and Entity ID)
4. Configure the IdP to release required attribute statements (at minimum: `uid` or whichever attribute maps to `client_id`)
5. Set `idp_metadata_url` to the IdP's metadata endpoint, or download the IdP metadata XML and use `idp_metadata_file`

### Security

- **Session cookies** are always `HttpOnly` (not configurable) to prevent XSS access
- **Relay state** is HMAC-signed to prevent CSRF/tampering on the return URL
- **`return_to`** parameter on `/saml/login` is validated as a relative path only — absolute URLs are rejected
- **Assertion replay protection**: Each assertion ID is tracked in a bounded TTL cache; reuse is rejected
- **IdP metadata auto-refresh**: When using `idp_metadata_url`, the runway periodically re-fetches metadata (default 24h) to handle IdP certificate rotation

### Troubleshooting

- **Clock skew errors**: The runway allows up to 180 seconds of clock skew (matching Shibboleth defaults). Ensure your server's clock is synchronized via NTP.
- **Certificate mismatch**: Verify that the IdP metadata contains the certificate the IdP is currently using to sign assertions. Use `metadata_refresh_interval` to auto-update.
- **Relay state invalid**: If users land on `/` after login instead of their original page, check that the session `signing_key` hasn't changed between the login redirect and the ACS callback.
- **"SAML assertion already consumed"**: This indicates a replay attempt or the user refreshed the ACS POST page. The assertion ID cache prevents reuse.

**Admin endpoint:** `GET /saml/stats` returns authentication counters (SSO attempts/successes/failures, token validations, session auths, logout requests).
