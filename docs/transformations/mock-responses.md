---
title: "Mock Responses"
sidebar_position: 16
---

The runway can return mock responses without calling the backend. Mocks can be static (manually configured body) or generated from OpenAPI specs. This is useful for development, testing, API prototyping, and graceful degradation.

## Configuration

### Static Mock Responses

```yaml
routes:
  - id: "mock-api"
    path: "/api/v2/status"
    backends:
      - url: "http://backend:9000"
    mock_response:
      enabled: true
      status_code: 200
      headers:
        Content-Type: "application/json"
        X-Mock: "true"
      body: '{"status":"ok","version":"2.0"}'
```

### Mock Responses from OpenAPI Specs

```yaml
routes:
  - id: "users-api"
    path: "/api/users/*"
    backends:
      - url: "http://users-backend:9000"
    openapi:
      spec_file: specs/users.yaml
    mock_response:
      enabled: true
      from_spec: true
      default_status: 200
      seed: 42
```

When `from_spec: true`, the runway generates mock responses from the OpenAPI spec's examples and schemas. No static `body` is needed.

## How It Works

When enabled, the mock response middleware intercepts the request at step 7.75 in the middleware chain (after WAF and fault injection, before body limits). The response is returned immediately — the request never reaches the backend.

### Static Mode

The configured `status_code`, `headers`, and `body` are returned as-is.

### Spec Mode (`from_spec: true`)

The mock resolver uses the following priority order:

1. **`Prefer: example=<name>` header**: Returns the named example from the spec.
2. **`Prefer: status=<code>` header**: Returns the response for the specified status code.
3. **Schema `example` field**: Uses the example value directly.
4. **Schema `examples` map**: Uses the first entry.
5. **Schema type/format**: Generates a fake value based on the schema type and format (email, uuid, date-time, integer, etc.).

Content negotiation uses the `Accept` header (defaults to JSON).

When `seed` is set to a non-zero value, generated values are deterministic — the same seed always produces the same output.

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable mock responses |
| `status_code` | int | `200` | HTTP status code to return (static mode) |
| `headers` | map | `{}` | Response headers to set |
| `body` | string | `""` | Response body string (static mode) |
| `from_spec` | bool | `false` | Generate responses from OpenAPI spec |
| `default_status` | int | `200` | Which response status to mock from spec |
| `seed` | int | `0` | Deterministic seed for fake data (0 = random) |

## Validation

- `status_code` must be between 100 and 599 (if set)
- `default_status` must be between 100 and 599 (if set)
- Cannot be combined with `echo: true` on the same route (mutually exclusive)
- `from_spec: true` requires `openapi.spec_file` or `openapi.spec_id` on the same route
- `from_spec: true` is mutually exclusive with `body` (cannot have both static body and spec generation)

## Admin API

**`GET /mock-responses`** returns per-route stats:

```json
{
  "mock-api": {
    "enabled": true,
    "status_code": 200,
    "served": 156
  }
}
```

## Use Cases

- **API prototyping**: Define response shapes before backends are ready
- **Spec-driven development**: Generate realistic mock responses from your OpenAPI spec
- **Testing**: Return known responses for integration test environments
- **Graceful degradation**: Serve cached/static fallback when backends are down
- **Development**: Mock third-party APIs locally

## See Also

- [OpenAPI Validation](validation.md) — How OpenAPI specs are loaded
- [Configuration Reference](../reference/configuration-reference.md#mock-response) — Config field reference
