# Security

The gateway provides IP filtering, CORS handling, a web application firewall (WAF), request body size limits, replay prevention, and custom DNS resolution for defense-in-depth security.

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

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `ip_filter.enabled` | bool | Enable IP filtering |
| `ip_filter.allow` | []string | Allowed CIDR blocks |
| `ip_filter.deny` | []string | Denied CIDR blocks |
| `ip_filter.order` | string | `allow_first` (default) or `deny_first` |
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

See [Configuration Reference](configuration-reference.md#security) for all fields.
