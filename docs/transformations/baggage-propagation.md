---
title: "Baggage Propagation"
sidebar_position: 13
---

Baggage propagation injects contextual key-value pairs into request headers forwarded to backends. These "baggage tags" can be extracted from various sources (headers, JWT claims, query parameters, cookies, or static values) and attached as headers on the upstream request. The middleware also supports W3C Trace Context propagation and W3C Baggage standard headers.

## Overview

In microservice architectures, backend services often need context about the originating request that is not present in the raw forwarded request. Baggage propagation solves this by extracting values from the incoming request and injecting them as headers on the outbound proxy request.

Common use cases:
- Propagating tenant IDs from JWT claims to backend headers
- Forwarding correlation IDs across service boundaries
- Injecting environment or deployment tags for observability
- Passing user attributes from authentication to downstream services
- Propagating W3C `traceparent`/`tracestate` headers to backends for distributed tracing
- Constructing W3C `baggage` headers for standards-compliant context propagation

## Configuration

Baggage can be configured globally (as defaults) and per-route (overrides global):

```yaml
# Global defaults
baggage:
  enabled: true
  propagate_trace: true
  w3c_baggage: true
  tags:
    - name: "environment"
      source: "static:production"
      header: "X-Environment"

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
      propagate_trace: true
      w3c_baggage: true
      tags:
        - name: "tenant-id"
          source: "jwt_claim:tenant_id"
          header: "X-Tenant-ID"
          baggage_key: "tenant.id"
        - name: "environment"
          source: "static:production"
          header: "X-Environment"
```

### Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable baggage propagation |
| `propagate_trace` | bool | `false` | Inject `traceparent`/`tracestate` headers into backend requests via OTEL propagator |
| `w3c_baggage` | bool | `false` | Construct W3C `baggage` header from tag values |
| `tags` | list | `[]` | List of baggage tag definitions |
| `tags[].name` | string | required | Logical name for the tag (used in variable context and as default W3C baggage key) |
| `tags[].source` | string | required | Source expression for the tag value (see Source Types below) |
| `tags[].header` | string | required* | Backend header name to propagate as. *Optional when `w3c_baggage: true` — tags without a header are W3C-only |
| `tags[].baggage_key` | string | tag name | W3C baggage key override. Must be unique, no spaces/commas/semicolons/equals/double-quotes |

### Source Types

| Source | Syntax | Description |
|--------|--------|-------------|
| `header:<name>` | `header:X-Source` | Extract value from an incoming request header |
| `jwt_claim:<name>` | `jwt_claim:tenant_id` | Extract value from a JWT claim (requires auth to be enabled) |
| `query:<name>` | `query:version` | Extract value from a URL query parameter |
| `cookie:<name>` | `cookie:region` | Extract value from a request cookie |
| `static:<value>` | `static:production` | Inject a fixed static value |

When a source value is empty or absent (e.g., the header does not exist, the JWT claim is missing, or the query parameter is not present), the tag is silently skipped — neither the custom header nor the W3C baggage entry is set.

For `jwt_claim` sources, authentication must be configured on the route (`auth.required: true` with a JWT-based method). The claims are read from the parsed identity context after the auth middleware has run.

## Global Defaults and Per-Route Merge

Baggage can be configured at the global level as defaults. Per-route config overrides global via `MergeNonZero`:

- **Per-route `tags` replaces global `tags` entirely** (not additive). This is standard `MergeNonZero` slice behavior — if the per-route config has any tags, they replace the global list completely.
- Bool fields (`propagate_trace`, `w3c_baggage`) use overlay-wins semantics.
- If a route has `baggage.enabled: true`, its config is merged with global. If only global has `baggage.enabled: true`, all routes get the global config.

## Trace Context Propagation

When `propagate_trace: true`, the middleware sets a flag on the request context. The proxy layer then uses the OTEL composite propagator to inject `traceparent`, `tracestate`, and `baggage` headers into the outbound backend request.

This requires `tracing.enabled: true` in the global config (validated at config load time). The injection happens at the transport boundary in the proxy's `createProxyRequest()`, ensuring trace headers are not visible to intermediate middleware.

## W3C Baggage Header

When `w3c_baggage: true`, tag values are assembled into a standards-compliant W3C `baggage` header using the OTEL baggage API. The middleware:

1. Reads any existing upstream baggage from the request context (preserving entries from upstream services)
2. Sets each tag value as a baggage member using `baggage.SetMember()` (runway keys override upstream if they conflict)
3. Stores the updated baggage in the Go context via `baggage.ContextWithBaggage()`
4. The OTEL propagator serializes this into the `baggage` HTTP header during proxy injection (when `propagate_trace: true`)

Each tag's W3C baggage key is determined by `baggage_key` if set, otherwise `name`. Keys must be valid W3C token characters (no spaces, commas, semicolons, equals, or double-quotes).

Tags with empty extracted values are skipped. Values that aren't W3C-encodable are silently skipped.

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

Trace context injection happens later, at the proxy transport layer (step 10), after all middleware has run.

## Admin API

### GET `/baggage`

Returns per-route baggage propagation stats.

```bash
curl http://localhost:8081/baggage
```

**Response (200 OK):**
```json
{
  "my-api": {
    "tags": 2,
    "propagated": 1542,
    "propagate_trace": true,
    "w3c_baggage": true
  }
}
```

## Validation

- `propagate_trace: true` requires `tracing.enabled: true` in global config
- At least one tag **or** `propagate_trace: true` is required when baggage is enabled
- `w3c_baggage: true` requires at least one tag
- Each tag must have `name` and `source`
- `header` is required unless `w3c_baggage: true` (W3C-only tags have no custom header)
- W3C baggage keys (resolved from `baggage_key` or `name`) must be unique and contain only valid W3C token characters
- Source must use a valid prefix (`header:`, `jwt_claim:`, `query:`, `cookie:`, `static:`)

## Examples

### Trace context propagation only

Forward distributed tracing headers to backends without any custom tags:

```yaml
tracing:
  enabled: true
  exporter: "otlp"
  endpoint: "otel-collector:4317"
  service_name: "my-runway"

routes:
  - id: "traced-api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:8080"
    baggage:
      enabled: true
      propagate_trace: true
```

### W3C baggage with custom headers

Propagate tags as both custom headers and W3C `baggage` header entries:

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
      propagate_trace: true
      w3c_baggage: true
      tags:
        - name: "tenant-id"
          source: "jwt_claim:tenant_id"
          header: "X-Tenant-ID"
          baggage_key: "tenant.id"
        - name: "user-id"
          source: "jwt_claim:sub"
          header: "X-User-ID"
```

This produces:
- Custom headers: `X-Tenant-ID: acme`, `X-User-ID: user-42`
- W3C baggage header: `baggage: tenant.id=acme,user-id=user-42`
- Trace headers: `traceparent: 00-...`, `tracestate: ...`

### W3C-only tags (no custom header)

Tags that only appear in the W3C `baggage` header, without a custom backend header:

```yaml
routes:
  - id: "w3c-api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:8080"
    baggage:
      enabled: true
      propagate_trace: true
      w3c_baggage: true
      tags:
        - name: "environment"
          source: "static:production"
        - name: "region"
          source: "header:X-Region"
```

### Global defaults with per-route override

Set global defaults and override per-route:

```yaml
baggage:
  enabled: true
  propagate_trace: true
  tags:
    - name: "env"
      source: "static:production"
      header: "X-Environment"

routes:
  - id: "api-v1"
    path: "/api/v1"
    path_prefix: true
    backends:
      - url: "http://backend:8080"
    # Inherits global baggage config (propagate_trace + env tag)

  - id: "api-v2"
    path: "/api/v2"
    path_prefix: true
    backends:
      - url: "http://backend-v2:8080"
    baggage:
      enabled: true
      propagate_trace: true
      w3c_baggage: true
      tags:
        # Per-route tags REPLACE global tags entirely
        - name: "tenant"
          source: "jwt_claim:tenant_id"
          header: "X-Tenant"
          baggage_key: "tenant.id"
```
