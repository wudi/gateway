---
title: "OPA Policy Engine"
sidebar_position: 4
---

The OPA (Open Policy Agent) middleware evaluates authorization decisions against an external OPA server. Each request is checked against a configured policy before being forwarded to the backend.

## Configuration

Per-route:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    opa:
      enabled: true
      url: "http://opa:8181"
      policy_path: "authz/allow"
      timeout: 5s
      fail_open: false
      include_body: false
      cache_ttl: 30s
      headers:
        - "Authorization"
        - "X-Tenant-ID"
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `opa.enabled` | bool | false | Enable OPA policy evaluation |
| `opa.url` | string | -- | OPA server base URL (required) |
| `opa.policy_path` | string | -- | Policy path (e.g., `authz/allow`) |
| `opa.timeout` | duration | 5s | Request timeout to OPA |
| `opa.fail_open` | bool | false | Allow requests when OPA is unreachable |
| `opa.include_body` | bool | false | Include request body in OPA input |
| `opa.cache_ttl` | duration | 0 | Cache policy decisions for this duration |
| `opa.headers` | []string | -- | Request headers to include in OPA input |

## How It Works

1. The middleware posts a JSON input document to `{url}/v1/data/{policy_path}`.
2. The input includes: `method`, `path`, `source_ip`, `headers` (filtered by the `headers` list), and `identity` (from authentication context).
3. When `include_body: true`, the request body is also included.
4. If the policy returns `true`, the request proceeds. If `false`, the request is rejected with `403 Forbidden`.

## Fail Modes

- **`fail_open: false`** (default) -- If OPA is unreachable or returns an error, the request is rejected with `503 Service Unavailable`.
- **`fail_open: true`** -- If OPA is unreachable, the request is allowed through. Use this for non-critical policies where availability is prioritized over enforcement.

## Response Caching

When `cache_ttl` is set, policy decisions are cached by a key derived from the input. This reduces load on the OPA server for repeated identical requests.

## Admin Endpoint

`GET /opa` returns per-route OPA policy evaluation statistics.

```bash
curl http://localhost:8081/opa
```

See [Configuration Reference](../reference/configuration-reference.md#opa-per-route) for field details.
