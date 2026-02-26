---
title: "Schema Evolution Validation"
sidebar_position: 8
---

The gateway can detect breaking changes in OpenAPI specs during config reloads. When a spec changes, the schema evolution checker compares the new version against the previously stored version and reports (or blocks) incompatible changes.

## Configuration

Schema evolution is configured under the `openapi` section:

```yaml
openapi:
  schema_evolution:
    enabled: true
    mode: warn
    store_dir: /tmp/gw-spec-history
    max_versions: 10
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable schema evolution checking |
| `mode` | string | `"warn"` | Action on breaking changes: `warn` (log only) or `block` (reject reload) |
| `store_dir` | string | `""` | Directory for storing spec version history |
| `max_versions` | int | `10` | Maximum number of spec versions to retain per spec ID |

## How It Works

### Startup

On first startup, the gateway stores the initial version of each OpenAPI spec. No comparison is performed on the first load since there is no previous version to compare against.

### Reload

During a config reload (after `buildState()` succeeds but before the state swap):

1. The checker loads each OpenAPI spec from the new config.
2. For each spec, it compares against the last stored version.
3. Breaking changes are detected and logged.
4. In `warn` mode: the reload proceeds, breaking changes are logged as warnings.
5. In `block` mode: the reload is rejected and the `ReloadResult` contains the error.

### Breaking Change Detection

The following changes are detected as breaking:

| Change | Description |
|--------|-------------|
| Endpoint removed | A path that existed in the old spec is missing from the new spec |
| Method removed | An HTTP method on an existing path was removed |
| Required parameter added | A new required parameter was added to an operation |
| Parameter type changed | The type of an existing parameter was changed |
| Enum value removed | A value was removed from a parameter or schema enum |
| Required request field added | A new required field was added to a request body schema |
| Required response field removed | A required field was removed from a response body schema |

### Version Storage

Spec versions are stored as JSON files in the configured `store_dir`. Each spec gets its own subdirectory. When `max_versions` is exceeded, the oldest version is pruned.

## Admin API

### `GET /schema-evolution`

Returns all compatibility reports:

```bash
curl http://localhost:8081/schema-evolution
```

**Response:**

```json
{
  "specs/users.yaml": {
    "spec_id": "specs/users.yaml",
    "old_version": "1.0.0",
    "new_version": "1.1.0",
    "compatible": false,
    "breaking_changes": [
      {
        "type": "required_param_added",
        "path": "/users",
        "method": "GET",
        "description": "Required parameter added: X-Tenant-ID"
      }
    ],
    "checked_at": "2026-01-15T10:30:00Z"
  }
}
```

### `GET /schema-evolution/{specID}`

Returns the detailed report for a specific spec:

```bash
curl http://localhost:8081/schema-evolution/specs-users-yaml
```

Returns `404` if no report exists for the given spec ID.

## Validation

- `mode` must be `"warn"` or `"block"`
- `max_versions` must be >= 0

## Example: Blocking Breaking Changes

```yaml
openapi:
  schema_evolution:
    enabled: true
    mode: block
    store_dir: /var/lib/runway/spec-history
    max_versions: 5
  specs:
    - id: users-api
      file: specs/users.yaml

routes:
  - id: users
    path: /api/users/*
    openapi:
      spec_file: specs/users.yaml
    backends:
      - url: http://users-backend:8080
```

With this configuration, if `specs/users.yaml` is updated during a reload with breaking changes (e.g., a required parameter is added), the reload will be rejected.

## See Also

- [OpenAPI Validation](../transformations/validation.md) — How OpenAPI specs are loaded
- [Configuration Reference](../reference/configuration-reference.md#openapi) — OpenAPI config fields
- [Admin API Reference](../reference/admin-api.md#schema-evolution) — Admin endpoint reference
