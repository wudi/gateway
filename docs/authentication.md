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

See [Configuration Reference](configuration-reference.md#authentication) for all fields.
