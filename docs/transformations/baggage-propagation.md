---
title: "Baggage Propagation"
sidebar_position: 13
---

Baggage propagation injects contextual key-value pairs into request headers forwarded to backends. These "baggage tags" can be extracted from various sources (headers, JWT claims, query parameters, cookies, or static values) and attached as headers on the upstream request, enabling distributed tracing context, tenant identification, and cross-service metadata propagation.

## Overview

In microservice architectures, backend services often need context about the originating request that is not present in the raw forwarded request. Baggage propagation solves this by extracting values from the incoming request and injecting them as headers on the outbound proxy request.

Common use cases:
- Propagating tenant IDs from JWT claims to backend headers
- Forwarding correlation IDs across service boundaries
- Injecting environment or deployment tags for observability
- Passing user attributes from authentication to downstream services

## Configuration

Baggage is configured per-route:

```yaml
routes:
  - id: "my-api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    auth:
      required: true
      methods: ["jwt"]
    baggage:
      enabled: true
      tags:
        - name: "X-Tenant-ID"
          source: "jwt_claim:tenant_id"
        - name: "X-Request-Source"
          source: "header:X-Source"
        - name: "X-Environment"
          source: "static:production"
        - name: "X-User-Region"
          source: "cookie:region"
        - name: "X-API-Version"
          source: "query:version"
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable baggage propagation for this route |
| `tags` | list | `[]` | List of baggage tag definitions |
| `tags[].name` | string | required | Header name to inject on the upstream request |
| `tags[].source` | string | required | Source expression for the tag value (see Source Types below) |

### Source Types

| Source | Syntax | Description |
|--------|--------|-------------|
| `header:<name>` | `header:X-Source` | Extract value from an incoming request header |
| `jwt_claim:<name>` | `jwt_claim:tenant_id` | Extract value from a JWT claim (requires auth to be enabled) |
| `query:<name>` | `query:version` | Extract value from a URL query parameter |
| `cookie:<name>` | `cookie:region` | Extract value from a request cookie |
| `static:<value>` | `static:production` | Inject a fixed static value |

When a source value is empty or absent (e.g., the header does not exist, the JWT claim is missing, or the query parameter is not present), the tag is silently skipped and the header is not injected.

For `jwt_claim` sources, authentication must be configured on the route (`auth.required: true` with a JWT-based method). The claims are read from the parsed identity context after the auth middleware has run.

## Pipeline Position

Baggage propagation runs at step 6.55 in the per-route middleware chain, after authentication and priority admission but before request rules:

```
... → authMW (6) → tokenRevokeMW (6.05) → claimsPropMW (6.15) → extAuthMW (6.25) →
nonceMW (6.3) → csrfMW (6.35) → idempotencyMW (6.4) → priorityMW (6.5) →
baggageMW (6.55) → requestRulesMW (7) → ...
```

This ensures that:
- JWT claims are available (auth has already run)
- Client identity is resolved (for `jwt_claim` sources)
- Request rules can inspect the injected baggage headers

## Admin API

### GET `/baggage`

Returns per-route baggage propagation configuration.

```bash
curl http://localhost:8081/baggage
```

**Response (200 OK):**
```json
{
  "my-api": {
    "enabled": true,
    "tags": [
      {"name": "X-Tenant-ID", "source": "jwt_claim:tenant_id"},
      {"name": "X-Request-Source", "source": "header:X-Source"},
      {"name": "X-Environment", "source": "static:production"}
    ]
  }
}
```

## Examples

### Tenant propagation from JWT

Extract a tenant identifier from the JWT `tenant_id` claim and forward it as `X-Tenant-ID`:

```yaml
routes:
  - id: "tenant-api"
    path: "/api/v1"
    path_prefix: true
    backends:
      - url: "http://api-backend:8080"
    auth:
      required: true
      methods: ["jwt"]
    baggage:
      enabled: true
      tags:
        - name: "X-Tenant-ID"
          source: "jwt_claim:tenant_id"
        - name: "X-User-ID"
          source: "jwt_claim:sub"
```

### Static environment tagging

Inject a static environment label so backends can distinguish between gateway environments:

```yaml
routes:
  - id: "backend-api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    baggage:
      enabled: true
      tags:
        - name: "X-Gateway-Env"
          source: "static:staging"
        - name: "X-Gateway-Region"
          source: "static:us-east-1"
```

### Multi-source propagation

Combine multiple source types on a single route:

```yaml
routes:
  - id: "rich-context"
    path: "/api/orders"
    path_prefix: true
    backends:
      - url: "http://orders:8080"
    auth:
      required: true
      methods: ["jwt"]
    baggage:
      enabled: true
      tags:
        - name: "X-Correlation-ID"
          source: "header:X-Correlation-ID"
        - name: "X-Tenant-ID"
          source: "jwt_claim:org_id"
        - name: "X-Client-Region"
          source: "cookie:user_region"
        - name: "X-API-Version"
          source: "query:v"
        - name: "X-Gateway-Instance"
          source: "static:gw-01"
```
