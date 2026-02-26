---
title: "Validation"
sidebar_position: 14
---

The runway provides two complementary validation systems: **JSON Schema validation** for standalone request/response body validation, and **OpenAPI validation** for full OpenAPI 3.x spec-based request/response validation with automatic route generation.

## JSON Schema Validation

Per-route request and response body validation using full JSON Schema via `santhosh-tekuri/jsonschema/v6`. Supports draft 4, 6, 7, 2019-09, and 2020-12 features including `minLength`, `pattern`, `enum`, `$ref`, `oneOf`/`anyOf`/`allOf`, nested objects, and more.

### Configuration

```yaml
routes:
  - id: create-user
    path: /users
    methods: [POST]
    backends:
      - url: http://localhost:8080
    validation:
      enabled: true
      schema: |
        {
          "type": "object",
          "required": ["name", "email"],
          "properties": {
            "name": {"type": "string", "minLength": 1},
            "email": {"type": "string", "pattern": "^[^@]+@[^@]+$"},
            "role": {"type": "string", "enum": ["admin", "user", "viewer"]}
          },
          "additionalProperties": false
        }
      response_schema: |
        {
          "type": "object",
          "required": ["id", "name"],
          "properties": {
            "id": {"type": "integer"},
            "name": {"type": "string"}
          }
        }
      log_only: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable validation |
| `schema` | string | | Inline JSON schema for request body |
| `schema_file` | string | | Path to JSON schema file for request body |
| `response_schema` | string | | Inline JSON schema for response body |
| `response_schema_file` | string | | Path to JSON schema file for response body |
| `log_only` | bool | `false` | Log validation errors instead of rejecting |

**Constraints:** `schema` and `schema_file` are mutually exclusive. `response_schema` and `response_schema_file` are mutually exclusive.

### Behavior

- **Request validation** runs at middleware step 9 (after bandwidth limiting, before GraphQL). Invalid requests receive `400 Bad Request` with error details.
- **Response validation** runs at step 17.5 (closest to the proxy). Invalid responses receive `502 Bad Gateway`. Response bodies up to 1MB are buffered for validation; larger bodies skip validation and stream through.
- **Log-only mode** logs validation errors but allows the request/response to proceed normally.

## OpenAPI Validation

Full OpenAPI 3.x spec-based request/response validation via `getkin/kin-openapi`. Validates path parameters, query parameters, request body, and response body against the spec.

### Per-Route Configuration

Attach OpenAPI validation to individual routes:

```yaml
routes:
  - id: create-pet
    path: /pets
    methods: [POST]
    backends:
      - url: http://localhost:8080
    openapi:
      spec_file: specs/petstore.yaml
      operation_id: createPet
      validate_request: true
      validate_response: false
      log_only: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `spec_file` | string | | Path to OpenAPI 3.x spec file |
| `spec_id` | string | | Reference to a top-level spec by ID |
| `operation_id` | string | | The operationId to validate against |
| `validate_request` | bool | `true` | Validate requests against the spec |
| `validate_response` | bool | `false` | Validate responses against the spec |
| `log_only` | bool | `false` | Log errors instead of rejecting |

**Constraints:** `spec_file` and `spec_id` are mutually exclusive. When using `spec_id`, it must reference an ID defined in the top-level `openapi.specs` section.

### Automatic Route Generation

Define OpenAPI specs at the top level to auto-generate routes for every path and operation in the spec:

```yaml
openapi:
  specs:
    - id: petstore
      file: specs/petstore.yaml
      default_backends:
        - url: http://petstore-backend:8080
          weight: 1
      route_prefix: /api
      strip_prefix: true
      validation:
        request: true
        response: false
        log_only: false
```

This generates routes for each path+method in the spec:
- Route ID: `openapi-{operationId}` (or `openapi-{method}-{sanitized-path}` if no operationId)
- Path: `route_prefix` + spec path (e.g., `/api/pets`)
- Methods: the HTTP method for the operation
- Backends: `default_backends` from the spec config
- OpenAPI validation auto-configured per the `validation` settings

Generated routes are appended to the `routes` list during config loading and follow the same middleware pipeline as manual routes.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `id` | string | required | Unique identifier for the spec |
| `file` | string | required | Path to OpenAPI 3.x spec file |
| `default_backends` | []BackendConfig | required | Backends for generated routes |
| `route_prefix` | string | `""` | Prefix prepended to all paths |
| `strip_prefix` | bool | `false` | Strip prefix before proxying |
| `validation.request` | *bool | `true` | Validate requests |
| `validation.response` | bool | `false` | Validate responses |
| `validation.log_only` | bool | `false` | Log-only mode |

### Middleware Chain Position

- **OpenAPI request validation** runs at step 9.1 (after JSON Schema validation, before GraphQL).
- **Response validation** (both JSON Schema and OpenAPI) runs at step 17.5, wrapping the proxy as the innermost middleware.

### Authentication

OpenAPI validation uses a no-op authentication function — The runway's own auth middleware handles authentication before the validation step.

## Admin API

### GET `/openapi`

Returns per-route OpenAPI validation status and metrics.

```bash
curl http://localhost:8081/openapi
```

```json
{
  "create-pet": {
    "validate_request": true,
    "validate_response": false,
    "log_only": false,
    "metrics": {
      "requests_validated": 150,
      "requests_failed": 3,
      "responses_validated": 0,
      "responses_failed": 0
    }
  }
}
```

Validation stats are also exposed in the `/dashboard` response under the `features.openapi` and `features.validation` keys.
