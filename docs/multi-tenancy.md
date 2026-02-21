# Multi-Tenancy

Multi-tenancy enables per-tenant resource governance: tenant identification, rate limiting, quota enforcement, and route access control. Tenants are defined in configuration and identified from request attributes (header, JWT claim, or client ID).

## How It Works

1. The tenant middleware extracts a tenant identifier from each request using the configured key
2. The identifier is matched against the tenant map; unknown tenants fall back to `default_tenant` or are rejected
3. Route ACL is checked (both tenant-to-route and route-to-tenant restrictions)
4. Per-tenant rate limit is enforced (if configured)
5. Per-tenant quota is enforced (if configured)
6. Tenant info is stored in request context and propagated to backends via headers

## Configuration

### Global Tenants Config

```yaml
tenants:
  enabled: true
  key: "header:X-Tenant-ID"
  default_tenant: "default"
  tenants:
    acme:
      rate_limit:
        rate: 100
        period: 1s
        burst: 200
      quota:
        limit: 100000
        period: monthly
      routes:
        - api-v2
        - dashboard
      metadata:
        plan: enterprise
        region: us-east-1
    startup:
      rate_limit:
        rate: 10
        period: 1s
        burst: 20
      quota:
        limit: 10000
        period: monthly
      metadata:
        plan: free
    default:
      rate_limit:
        rate: 5
        period: 1s
        burst: 10
      quota:
        limit: 1000
        period: monthly
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable multi-tenancy |
| `key` | string | - | Tenant identification key (required) |
| `default_tenant` | string | - | Fallback tenant for unknown identifiers (empty = reject) |
| `tenants` | map | - | Tenant definitions (at least one required) |

### Tenant Identification Keys

| Key | Description |
|-----|-------------|
| `header:<name>` | Value of a request header |
| `jwt_claim:<name>` | Value of a JWT claim (requires auth) |
| `client_id` | Authenticated client ID from JWT |

### Per-Tenant Config

| Field | Type | Description |
|-------|------|-------------|
| `rate_limit.rate` | int | Requests per period |
| `rate_limit.period` | duration | Rate limit window |
| `rate_limit.burst` | int | Maximum burst (defaults to rate) |
| `quota.limit` | int | Maximum requests per quota period |
| `quota.period` | string | `hourly`, `daily`, `monthly`, or `yearly` |
| `routes` | []string | Allowed route IDs (empty = all routes) |
| `max_body_size` | int | Reserved for future use |
| `priority` | int | Reserved for future use |
| `metadata` | map | Key-value pairs propagated as `X-Tenant-*` headers |

### Per-Route Tenant Config

```yaml
routes:
  - id: api-v2
    path: /api/v2
    backends:
      - url: http://backend:8080
    tenant:
      required: true
      allowed:
        - acme
        - startup
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `required` | bool | `false` | Reject requests without a resolved tenant |
| `allowed` | []string | - | Restrict route to specific tenant IDs |

## Middleware Position

Step 6.6 in the per-route middleware chain -- after all auth sub-steps (6.0-6.55) and before request rules (step 7). This ensures JWT claims and client ID are available for tenant identification.

## Request Headers

The tenant middleware propagates metadata as request headers for downstream backends:

- `X-Tenant-ID` is set as a response header
- Each metadata key-value pair is sent as `X-Tenant-{Key}: {Value}` request header

## Admin API

```
GET /tenants
```

Returns tenant statistics:

```json
{
  "enabled": true,
  "tenant_count": 3,
  "tenants": {
    "acme": {
      "allowed": 15230,
      "rejected": 12,
      "rate_limited": 0,
      "quota_exceeded": 0
    },
    "startup": {
      "allowed": 4500,
      "rejected": 120,
      "rate_limited": 5,
      "quota_exceeded": 0
    }
  }
}
```

## Example: Header-Based Tenant Isolation

```yaml
tenants:
  enabled: true
  key: "header:X-Tenant-ID"
  tenants:
    tenant-a:
      rate_limit:
        rate: 50
        period: 1s
        burst: 100
      routes:
        - api
    tenant-b:
      rate_limit:
        rate: 100
        period: 1s
        burst: 200

routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    tenant:
      required: true
```

## Example: JWT Claim-Based Tenancy

```yaml
tenants:
  enabled: true
  key: "jwt_claim:org_id"
  default_tenant: "free"
  tenants:
    enterprise:
      quota:
        limit: 1000000
        period: yearly
    free:
      quota:
        limit: 10000
        period: monthly

routes:
  - id: premium-api
    path: /premium
    backends:
      - url: http://premium-backend:8080
    auth:
      required: true
    tenant:
      required: true
      allowed:
        - enterprise
```
