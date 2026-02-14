# Security

The gateway provides trusted proxy handling, IP filtering, geo filtering, CORS handling, a web application firewall (WAF), request body size limits, replay prevention, CSRF protection, backend request signing, security response headers, and custom DNS resolution for defense-in-depth security.

## Trusted Proxies

When the gateway runs behind a load balancer, CDN, or reverse proxy, the real client IP is in the `X-Forwarded-For` or `X-Real-IP` headers — not in the TCP connection address. Without trusted proxy configuration, any client can spoof these headers and bypass IP-based security features (rate limiting, IP filtering, geo filtering).

Configure `trusted_proxies` to tell the gateway which upstream proxies to trust. The gateway walks the `X-Forwarded-For` chain from right to left, skipping trusted proxy IPs, and uses the first untrusted IP as the real client IP.

```yaml
trusted_proxies:
  cidrs:
    - "10.0.0.0/8"         # internal network
    - "172.16.0.0/12"       # Docker networks
    - "192.168.0.0/16"      # private networks
    - "127.0.0.1"           # loopback
  headers:                   # headers to check (default: X-Forwarded-For, X-Real-IP)
    - "X-Forwarded-For"
    - "X-Real-IP"
  max_hops: 3                # max proxy hops to walk back (0 = unlimited)
```

### How It Works

1. **Check RemoteAddr** — If the direct TCP connection source does not match any trusted CIDR, the gateway uses `RemoteAddr` as the client IP (ignoring all headers). This prevents spoofing from untrusted sources.
2. **Walk XFF chain** — If `RemoteAddr` is trusted, the gateway reads the configured headers (default: `X-Forwarded-For`, then `X-Real-IP`) and walks the `X-Forwarded-For` chain from right to left, skipping IPs that match trusted CIDRs.
3. **Return first untrusted IP** — The first IP in the chain that does NOT match a trusted CIDR is the real client IP.

### Security Impact

All IP-based features automatically use the extracted real IP:
- **Rate limiting** — limits apply to the real client, not the load balancer
- **IP filtering** — allow/deny rules evaluate against the real client IP
- **Geo filtering** — country lookups use the real client location
- **WAF** — IP-based rules see the real client
- **Rules engine** — `ip.src` resolves to the real client IP
- **Access logging** — logs show the real client IP

### Without Trusted Proxies

When `trusted_proxies` is not configured, the gateway uses legacy behavior: it trusts the first entry in `X-Forwarded-For` unconditionally. This is acceptable when the gateway is the internet-facing edge (no upstream proxies), but is **insecure** when behind a load balancer because clients can spoof the header.

### Validation

- All entries in `cidrs` must be valid CIDR notation (e.g., `10.0.0.0/8`) or bare IP addresses (e.g., `127.0.0.1`)
- `max_hops` must be >= 0

## IP Filtering

Allow or deny requests by client IP using CIDR blocks. IP filtering can be configured globally and per route — both are evaluated (global first, then per-route). The `order` field controls evaluation order: `allow_first` checks allow rules before deny rules (default), while `deny_first` checks deny rules first.

```yaml
# Global IP filter
ip_filter:
  enabled: true
  order: "deny_first"         # "allow_first" (default) or "deny_first"
  allow:
    - "10.0.0.0/8"
    - "192.168.0.0/16"
  deny:
    - "10.0.0.100/32"

# Per-route override
routes:
  - id: "admin"
    path: "/admin"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    ip_filter:
      enabled: true
      allow:
        - "10.0.1.0/24"
```

Denied requests receive `403 Forbidden`.

## Geo Filtering

Block or allow requests based on the client's geographic location using MaxMind (`.mmdb`) or IPIP (`.ipdb`) databases. Geo filtering can be configured globally and per route — both are evaluated (global first, then per-route). The middleware also injects `X-Geo-Country` and `X-Geo-City` headers for downstream services.

```yaml
# Global geo config
geo:
  enabled: true
  database: "/etc/gateway/GeoLite2-City.mmdb"
  inject_headers: true       # inject X-Geo-Country / X-Geo-City
  deny_countries:
    - "CN"
    - "RU"
  order: "deny_first"        # "deny_first" (default) or "allow_first"

# Per-route override
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    geo:
      enabled: true
      allow_countries:
        - "US"
        - "CA"
        - "GB"
      order: "deny_first"
```

### Allow/Deny Logic

The `order` field controls evaluation order:

- **deny_first** (default): Check deny rules first — if matched, deny. Then check allow rules — if allow lists are non-empty and not matched, deny. Otherwise allow.
- **allow_first**: Check allow rules first — if allow lists are non-empty and matched, allow. Then check deny rules — if matched, deny. Otherwise allow.

Country codes must be ISO 3166-1 alpha-2 (e.g. `US`, `DE`, `CN`). City names are case-insensitive.

### Shadow Mode

Use `shadow_mode: true` to log geo decisions without rejecting traffic — useful for testing rules before enforcement:

```yaml
geo:
  enabled: true
  database: "/etc/gateway/GeoLite2-City.mmdb"
  deny_countries: ["CN"]
  shadow_mode: true
```

### Supported Databases

| Format | Extension | Library |
|--------|-----------|---------|
| MaxMind GeoIP2/GeoLite2 | `.mmdb` | `oschwald/maxminddb-golang/v2` |
| IPIP | `.ipdb` | `ipipdotnet/ipdb-go` |

Denied requests receive `451 Unavailable For Legal Reasons` with a JSON body.

## CORS

Configure Cross-Origin Resource Sharing headers and preflight handling per route:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    cors:
      enabled: true
      allow_origins:
        - "https://app.example.com"
      allow_origin_patterns:           # regex patterns
        - "https://.*\\.example\\.com"
      allow_methods: ["GET", "POST", "PUT", "DELETE"]
      allow_headers: ["Authorization", "Content-Type"]
      expose_headers: ["X-Request-ID"]
      allow_credentials: true
      allow_private_network: true      # Access-Control-Allow-Private-Network
      max_age: 3600                    # preflight cache (seconds)
```

Preflight (`OPTIONS`) requests are handled automatically and do not reach the backend.

## WAF (Web Application Firewall)

The WAF uses the [Coraza](https://coraza.io/) engine with ModSecurity-compatible rules. It can operate in `block` mode (reject malicious requests) or `detect` mode (log only).

WAF can be configured globally and per route:

```yaml
# Global WAF
waf:
  enabled: true
  mode: "block"
  sql_injection: true     # built-in SQLi protection
  xss: true               # built-in XSS protection
  rule_files:
    - "/etc/gateway/waf/custom-rules.conf"
  inline_rules:
    - 'SecRule REQUEST_URI "@contains /admin" "id:1001,phase:1,deny,status:403"'

# Per-route WAF
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    waf:
      enabled: true
      mode: "detect"      # log-only for this route
```

### Built-in Protections

The `sql_injection` and `xss` shortcuts enable curated rule sets without requiring external rule files.

## Request Body Size Limits

Limit the maximum request body size per route:

```yaml
routes:
  - id: "upload"
    path: "/upload"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    max_body_size: 10485760   # 10 MB (bytes)
```

Requests exceeding the limit receive `413 Request Entity Too Large`.

## Custom DNS Resolver

Override the system DNS resolver for backend connections. Useful for service mesh environments or split-horizon DNS:

```yaml
dns_resolver:
  nameservers:
    - "10.0.0.53:53"
    - "10.0.0.54:53"
  timeout: 5s
```

All backend connections use the custom resolver when configured.

## Replay Prevention

Prevent request replay attacks using nonce-based deduplication. Clients include a unique value in the `X-Nonce` header (configurable), and the gateway rejects duplicate nonces within a TTL window.

```yaml
nonce:
  enabled: true
  header: "X-Nonce"
  query_param: "nonce"   # optional: also check ?nonce=<value>
  ttl: 5m
  mode: "local"          # "local" or "distributed" (Redis)
  scope: "global"        # "global" or "per_client"
  required: true
```

The nonce is read from the header first; if absent and `query_param` is set, the query parameter is checked as a fallback. Duplicate requests receive `409 Conflict`. Missing nonces receive `400 Bad Request` when `required: true`. Optional timestamp validation via `timestamp_header` and `max_age` rejects stale requests.

See [Replay Prevention](replay-prevention.md) for full documentation including distributed mode, per-client scope, and timestamp validation.

## CSRF Protection

Prevent cross-site request forgery attacks using the double-submit cookie pattern with HMAC-signed tokens. State-changing requests must include a matching token in both a cookie and request header.

```yaml
csrf:
  enabled: true
  secret: "${CSRF_SECRET}"
  cookie_secure: true
  inject_token: true
  allowed_origins:
    - "https://app.example.com"
```

Optional Origin/Referer validation and shadow mode for gradual rollout. See [CSRF Protection](csrf.md) for full documentation.

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `ip_filter.enabled` | bool | Enable IP filtering |
| `ip_filter.allow` | []string | Allowed CIDR blocks |
| `ip_filter.deny` | []string | Denied CIDR blocks |
| `ip_filter.order` | string | `allow_first` (default) or `deny_first` |
| `geo.enabled` | bool | Enable geo filtering |
| `geo.database` | string | Path to `.mmdb` or `.ipdb` file (global only) |
| `geo.inject_headers` | bool | Inject `X-Geo-Country`/`X-Geo-City` headers |
| `geo.allow_countries` | []string | Allowed country codes (ISO 3166-1 alpha-2) |
| `geo.deny_countries` | []string | Denied country codes |
| `geo.allow_cities` | []string | Allowed city names (case-insensitive) |
| `geo.deny_cities` | []string | Denied city names |
| `geo.order` | string | `deny_first` (default) or `allow_first` |
| `geo.shadow_mode` | bool | Log but don't reject |
| `cors.allow_origin_patterns` | []string | Regex origin patterns |
| `cors.allow_private_network` | bool | Private network access header |
| `waf.mode` | string | `block` or `detect` |
| `waf.sql_injection` | bool | Enable built-in SQLi rules |
| `waf.xss` | bool | Enable built-in XSS rules |
| `max_body_size` | int64 | Max request body (bytes) |
| `dns_resolver.nameservers` | []string | DNS servers (host:port) |
| `nonce.enabled` | bool | Enable replay prevention |
| `nonce.header` | string | Nonce header name (default `X-Nonce`) |
| `nonce.ttl` | duration | Nonce TTL (default `5m`) |
| `nonce.mode` | string | `local` (default) or `distributed` |
| `nonce.scope` | string | `global` (default) or `per_client` |
| `nonce.required` | bool | Reject missing nonce (default `true`) |

## Upstream mTLS (Client Certificates)

The gateway can present client certificates when connecting to backends that require mutual TLS authentication. This is common for internal microservices, payment APIs, and partner integrations.

Configure client certificates globally or per-upstream via the `transport` block:

```yaml
# Global — all upstreams present this client cert
transport:
  ca_file: /etc/gateway/internal-ca.pem
  cert_file: /etc/gateway/client.crt
  key_file: /etc/gateway/client.key

# Per-upstream — specific cert for a single upstream
upstreams:
  payment-api:
    backends:
      - url: https://payments.internal:443
    transport:
      cert_file: /etc/gateway/payment-client.crt
      key_file: /etc/gateway/payment-client.key
      ca_file: /etc/gateway/payment-ca.pem
```

Both `cert_file` and `key_file` must be specified together. Per-upstream settings override global settings. See [Transport](transport.md) for details.

## Backend Request Signing

The gateway can HMAC-sign every outgoing request so backends can verify that requests actually came through the gateway and weren't tampered with. This prevents "backend bypass" attacks where clients send requests directly to backend services.

### How It Works

1. The gateway reads the request body (for POST/PUT/PATCH/DELETE) and computes its SHA-256 hash
2. A signing string is built from the method, path+query, timestamp, body hash, and any configured signed headers
3. An HMAC is computed over the signing string using the configured algorithm and shared secret
4. Four headers are injected into the outgoing request

### Signing String Format

Newline-separated fields:

```
POST
/api/v1/users?page=2
1707654321
e3b0c44298fc1c14...  (SHA-256 hex of body)
content-type:application/json
host:api.example.com
```

### Injected Headers

| Header | Example | Description |
|--------|---------|-------------|
| `X-Gateway-Signature` | `hmac-sha256=a1b2c3...` | HMAC hex digest with algorithm prefix |
| `X-Gateway-Timestamp` | `1707654321` | Unix seconds when signature was created |
| `X-Gateway-Key-ID` | `gateway-key-1` | Key identifier for rotation |
| `X-Gateway-Signed-Headers` | `content-type;host` | Semicolon-separated list of signed headers |

The header prefix is configurable via `header_prefix` (default `X-Gateway-`).

### Configuration

```yaml
# Global — signs all route requests
backend_signing:
  enabled: true
  algorithm: "hmac-sha256"       # or "hmac-sha512"
  secret: "base64-encoded-secret-at-least-32-bytes"
  key_id: "gateway-key-1"
  signed_headers:                # optional: headers to include
    - "Content-Type"
    - "Host"
  include_body: true             # default true
  header_prefix: "X-Gateway-"   # default "X-Gateway-"

# Per-route override
routes:
  - id: "payments"
    path: "/api/payments"
    backends:
      - url: "http://payments:8080"
    backend_signing:
      enabled: true
      key_id: "payments-key-2"   # override key for this route
      algorithm: "hmac-sha512"   # stronger algorithm for sensitive route
```

### Key Rotation

Use `key_id` to support key rotation. Deploy the new key to backends first, then update the gateway config. Backends should accept signatures from any known key ID during the transition period.

### Backend Verification Pseudocode

```python
def verify_request(request, secrets):
    key_id = request.headers["X-Gateway-Key-ID"]
    secret = secrets[key_id]
    timestamp = request.headers["X-Gateway-Timestamp"]

    # Reject stale signatures (e.g., > 5 minutes)
    if abs(time.now() - int(timestamp)) > 300:
        return False

    # Rebuild signing string
    body_hash = sha256(request.body).hex()
    signing_str = f"{request.method}\n{request.path_and_query}\n{timestamp}\n{body_hash}"

    signed_headers = request.headers["X-Gateway-Signed-Headers"]
    if signed_headers:
        for header in signed_headers.split(";"):
            signing_str += f"\n{header.lower()}:{request.headers[header]}"

    # Verify HMAC
    algo, sig = request.headers["X-Gateway-Signature"].split("=", 1)
    expected = hmac(secret, signing_str, algorithm=algo).hex()
    return constant_time_compare(sig, expected)
```

## Security Response Headers

Automatically inject security-related HTTP response headers on every response. This provides defense-in-depth without requiring rules engine expressions or manual header transforms. Configurable globally and per route (per-route overrides global).

```yaml
# Global security headers — applied to all routes
security_headers:
  enabled: true
  strict_transport_security: "max-age=31536000; includeSubDomains"
  content_security_policy: "default-src 'self'"
  x_content_type_options: "nosniff"     # default when omitted
  x_frame_options: "DENY"
  referrer_policy: "strict-origin-when-cross-origin"
  permissions_policy: "camera=(), microphone=(), geolocation=()"
  cross_origin_opener_policy: "same-origin"
  cross_origin_embedder_policy: "require-corp"
  cross_origin_resource_policy: "same-origin"
  x_permitted_cross_domain_policies: "none"
  custom_headers:
    X-Custom-Security: "enabled"
```

Per-route overrides:

```yaml
routes:
  - id: "web-app"
    path: "/app"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    security_headers:
      enabled: true
      x_frame_options: "SAMEORIGIN"   # override global DENY
      content_security_policy: "default-src 'self'; script-src 'self' cdn.example.com"
```

### Supported Headers

| Config Field | HTTP Header | Default |
|-------------|-------------|---------|
| `strict_transport_security` | `Strict-Transport-Security` | — |
| `content_security_policy` | `Content-Security-Policy` | — |
| `x_content_type_options` | `X-Content-Type-Options` | `nosniff` |
| `x_frame_options` | `X-Frame-Options` | — |
| `referrer_policy` | `Referrer-Policy` | — |
| `permissions_policy` | `Permissions-Policy` | — |
| `cross_origin_opener_policy` | `Cross-Origin-Opener-Policy` | — |
| `cross_origin_embedder_policy` | `Cross-Origin-Embedder-Policy` | — |
| `cross_origin_resource_policy` | `Cross-Origin-Resource-Policy` | — |
| `x_permitted_cross_domain_policies` | `X-Permitted-Cross-Domain-Policies` | — |
| `custom_headers` | Any | — |

`X-Content-Type-Options: nosniff` is always injected by default (even with no explicit value). All other headers are only sent when explicitly configured. Use `custom_headers` for arbitrary extra response headers.

Per-route metrics (total requests served, header count) are available via the `/security-headers` admin endpoint.

See [Configuration Reference](configuration-reference.md#security) for all fields.

## HTTPS Redirect

Automatically redirects HTTP requests to HTTPS. Runs in the global handler chain, before route matching.

```yaml
https_redirect:
  enabled: true
  port: 443            # target HTTPS port (default 443)
  permanent: false     # true=301, false=302 (default false)
```

The middleware checks both `r.TLS` and the `X-Forwarded-Proto` header, so it works correctly behind TLS-terminating load balancers. When `port` is 443, the port is omitted from the redirect URL for cleaner URLs.

Admin endpoint: `GET /https-redirect` returns redirect statistics.

**Note:** HTTPS redirect is part of the global handler chain, which is not rebuilt on config reload. Changes to `https_redirect` require a gateway restart.

## Allowed Hosts

Validates the `Host` header against a whitelist. Rejects requests to unknown hosts with `421 Misdirected Request`.

```yaml
allowed_hosts:
  enabled: true
  hosts:
    - "api.example.com"
    - "*.internal.example.com"    # wildcard: matches any subdomain
```

Exact hosts use O(1) map lookups. Wildcard patterns (`*.example.com`) match any subdomain via suffix comparison. The port is stripped before matching.

Admin endpoint: `GET /allowed-hosts` returns the host list and rejection count.

**Note:** Like HTTPS redirect, allowed hosts is part of the global handler chain and requires a restart to change.
