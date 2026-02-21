# Multi-Tenancy

Multi-tenancy enables per-tenant resource governance: tenant identification, rate limiting, quota enforcement, route access control, body size limits, priority admission, timeouts, circuit breaker isolation, cache isolation, backend routing, response headers, and usage analytics. Tenants are defined in configuration and identified from request attributes (header, JWT claim, or client ID). A tier/plan system provides defaults shared across tenants.

## How It Works

1. The tenant middleware extracts a tenant identifier from each request using the configured key
2. The identifier is matched against the tenant map; unknown tenants fall back to `default_tenant` or are rejected
3. Route ACL is checked (both tenant-to-route and route-to-tenant restrictions)
4. Per-tenant rate limit is enforced (if configured)
5. Per-tenant quota is enforced (if configured)
6. Per-tenant timeout applied via `context.WithTimeout` (uses lesser of route and tenant deadlines)
7. Tenant info stored in request context and propagated to backends via headers
8. Per-tenant custom response headers are set
9. Usage analytics (latency, bytes, status codes) are recorded

## Configuration

### Global Tenants Config

```yaml
tenants:
  enabled: true
  key: "header:X-Tenant-ID"
  default_tenant: "default"
  tiers:
    enterprise:
      rate_limit:
        rate: 1000
        period: 1s
        burst: 2000
      quota:
        limit: 1000000
        period: monthly
      max_body_size: 10485760  # 10MB
      priority: 2
      timeout: 30s
      metadata:
        support: premium
      response_headers:
        X-Plan: enterprise
    free:
      rate_limit:
        rate: 10
        period: 1s
        burst: 20
      quota:
        limit: 10000
        period: monthly
      max_body_size: 1048576  # 1MB
      priority: 8
      timeout: 5s
  tenants:
    acme:
      tier: enterprise
      routes:
        - api-v2
        - dashboard
      metadata:
        region: us-east-1
      response_headers:
        X-Custom-Header: acme-value
    startup:
      tier: free
      metadata:
        region: eu-west-1
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
| `tiers` | map | - | Tier/plan definitions with shared defaults |
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
| `tier` | string | Reference to a tier in `tiers` map; inherits tier defaults |
| `rate_limit.rate` | int | Requests per period |
| `rate_limit.period` | duration | Rate limit window |
| `rate_limit.burst` | int | Maximum burst (defaults to rate) |
| `quota.limit` | int | Maximum requests per quota period |
| `quota.period` | string | `hourly`, `daily`, `monthly`, or `yearly` |
| `routes` | []string | Allowed route IDs (empty = all routes) |
| `max_body_size` | int | Maximum request body size in bytes. Enforced as `min(route_limit, tenant_limit)` |
| `priority` | int | Priority level override (1-10, lower = higher priority). Overrides configured priority levels |
| `timeout` | duration | Per-tenant request timeout. Uses `context.WithTimeout` so the lesser of route and tenant deadlines applies |
| `metadata` | map | Key-value pairs propagated as `X-Tenant-*` headers |
| `response_headers` | map | Custom response headers set on every response for this tenant |

### Tier Config

Tiers define shared defaults for groups of tenants. A tenant referencing a tier inherits all tier values, with tenant-specific non-zero values taking precedence. For maps (`metadata`, `response_headers`), values are merged with tenant keys winning.

| Field | Type | Description |
|-------|------|-------------|
| `rate_limit` | object | Default rate limit for tenants in this tier |
| `quota` | object | Default quota for tenants in this tier |
| `max_body_size` | int | Default max body size |
| `priority` | int | Default priority level |
| `timeout` | duration | Default request timeout |
| `metadata` | map | Default metadata (merged with tenant metadata) |
| `response_headers` | map | Default response headers (merged with tenant headers) |

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
    tenant_backends:
      acme:
        - url: http://acme-dedicated:8080
      startup:
        - url: http://shared-pool:8080
        - url: http://shared-pool:8081
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tenant.required` | bool | `false` | Reject requests without a resolved tenant |
| `tenant.allowed` | []string | - | Restrict route to specific tenant IDs |
| `tenant_backends` | map | - | Per-tenant dedicated backend sets (tenant ID -> backend list) |

### Per-Tenant Circuit Breaker Isolation

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      timeout: 30s
      tenant_isolation: true
```

When `tenant_isolation: true`, each tenant gets its own circuit breaker instance. One tenant's failures won't trip the breaker for other tenants. Without tenant isolation, all tenants share the route-level breaker.

## Cache Isolation

When multi-tenancy is enabled, cache keys automatically include the tenant ID. This ensures tenants never see each other's cached responses.

## Middleware Position

Step 6.6 in the per-route middleware chain -- after all auth sub-steps (6.0-6.55) and before request rules (step 7). This ensures JWT claims and client ID are available for tenant identification.

## Request Headers

The tenant middleware propagates metadata as request headers for downstream backends:

- `X-Tenant-ID` is set as a response header
- Each metadata key-value pair is sent as `X-Tenant-{Key}: {Value}` request header

## Access Logging

When multi-tenancy is enabled, the `tenant_id` field is included in structured (JSON) log output for every request with a resolved tenant.

## Admin API

### List Tenants / Stats

```
GET /tenants
```

Returns tenant statistics including usage analytics:

```json
{
  "enabled": true,
  "tenant_count": 3,
  "tenants": {
    "acme": {
      "allowed": 15230,
      "rejected": 12,
      "rate_limited": 0,
      "quota_exceeded": 0,
      "analytics": {
        "request_count": 15230,
        "avg_latency_ms": 45.2,
        "bytes_in": 1523000,
        "bytes_out": 76150000,
        "status_1xx": 0,
        "status_2xx": 15000,
        "status_3xx": 100,
        "status_4xx": 118,
        "status_5xx": 12
      }
    }
  }
}
```

### Get Tenant

```
GET /tenants/{id}
```

Returns the config for a specific tenant.

### Create Tenant

```
POST /tenants/{id}
Content-Type: application/json

{
  "tier": "enterprise",
  "metadata": {"region": "us-west-2"},
  "response_headers": {"X-Custom": "value"}
}
```

Creates a new tenant at runtime. Returns 409 if the tenant already exists.

### Update Tenant

```
PUT /tenants/{id}
Content-Type: application/json

{
  "rate_limit": {"rate": 200, "period": "1s", "burst": 400}
}
```

Updates an existing tenant. Recreates rate limiter and quota enforcer. Returns 404 if the tenant doesn't exist.

### Delete Tenant

```
DELETE /tenants/{id}
```

Removes a tenant. Returns 404 if the tenant doesn't exist.

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

## Example: JWT Claim-Based Tenancy with Tiers

```yaml
tenants:
  enabled: true
  key: "jwt_claim:org_id"
  default_tenant: "free"
  tiers:
    enterprise:
      quota:
        limit: 1000000
        period: yearly
      max_body_size: 52428800  # 50MB
      priority: 2
      timeout: 60s
    free:
      quota:
        limit: 10000
        period: monthly
      max_body_size: 1048576  # 1MB
      priority: 8
      timeout: 10s
  tenants:
    acme-corp:
      tier: enterprise
      response_headers:
        X-Plan: enterprise
    starter-co:
      tier: free
    free:
      tier: free

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
        - acme-corp
```

## Example: Per-Tenant Backend Routing

```yaml
tenants:
  enabled: true
  key: "header:X-Tenant-ID"
  tenants:
    acme:
      tier: enterprise
    startup:
      tier: free

routes:
  - id: api
    path: /api
    backends:
      - url: http://shared-backend:8080
    tenant_backends:
      acme:
        - url: http://acme-dedicated-1:8080
        - url: http://acme-dedicated-2:8080
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      tenant_isolation: true
```

In this example, requests from the `acme` tenant are routed to dedicated backends, while all other tenants use the shared backend pool. Circuit breaker failures are isolated per tenant.
