---
title: "Developer Portal / API Catalog"
sidebar_position: 7
---

The gateway can serve a browsable API catalog from the admin port. It aggregates all configured routes and their OpenAPI specs into a JSON API and an embedded HTML UI powered by Redoc.

## Configuration

```yaml
admin:
  catalog:
    enabled: true
    title: "My API Gateway"
    description: "Internal API catalog"
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the API catalog on the admin port |
| `title` | string | `"API Gateway"` | Title displayed in the catalog UI |
| `description` | string | `""` | Description displayed in the catalog UI |

## How It Works

1. **Route Collection**: The catalog builder iterates over all registered routes and their corresponding `RouteConfig` entries.
2. **OpenAPI Discovery**: For routes with an `openapi.spec_file`, the builder retrieves the parsed spec document and extracts its title, version, and description.
3. **Tag Generation**: Each entry is tagged based on enabled features (gRPC, WebSocket, GraphQL, etc.).
4. **Stats Aggregation**: The builder counts total routes, total discovered specs, and total backend instances across all routes.
5. **On-Demand Build**: The catalog is rebuilt on each request to reflect the current state, including any specs loaded after startup or added via config reload.

Routes without OpenAPI specs still appear in the catalog — they just don't have a linked spec viewer or description.

## Endpoints

All catalog endpoints are served on the admin port (default `:8081`).

### `GET /catalog`

Returns the full catalog as JSON, including all routes, their backends, and associated OpenAPI specs.

```bash
curl http://localhost:8081/catalog
```

**Response:**
```json
{
  "title": "My API Gateway",
  "description": "Internal API catalog",
  "stats": {
    "total_routes": 3,
    "total_specs": 1,
    "total_backends": 5
  },
  "entries": [
    {
      "id": "users-api",
      "path": "/api/users/*",
      "methods": ["GET", "POST"],
      "description": "User management API",
      "tags": ["Auth Required"],
      "backends": 2,
      "spec_id": "specs-users-yaml",
      "auth": true
    },
    {
      "id": "grpc-api",
      "path": "/pkg.Service/*",
      "methods": ["POST"],
      "tags": ["gRPC"],
      "backends": 2,
      "auth": false,
      "grpc": true
    },
    {
      "id": "unified-graphql",
      "path": "/graphql",
      "methods": ["POST"],
      "tags": ["GraphQL Federation"],
      "backends": 1,
      "auth": false,
      "graphql": true
    }
  ],
  "specs": [
    {
      "id": "specs-users-yaml",
      "title": "Users API",
      "version": "1.0.0",
      "route_id": "users-api"
    }
  ]
}
```

### `GET /catalog/specs`

Lists all discovered OpenAPI specs.

```bash
curl http://localhost:8081/catalog/specs
```

**Response:**
```json
[
  {
    "id": "specs-users-yaml",
    "title": "Users API",
    "version": "1.0.0",
    "route_id": "users-api"
  }
]
```

### `GET /catalog/specs/{id}`

Returns the raw OpenAPI spec JSON for a specific spec ID.

```bash
curl http://localhost:8081/catalog/specs/specs-users-yaml
```

**Response:** The full OpenAPI 3.x document as JSON.

Returns `404 Not Found` if the spec ID does not exist.

### `GET /catalog/ui`

Serves an HTML page listing all APIs in the catalog. Each entry with an associated OpenAPI spec links to a per-spec documentation viewer. The page title comes from the catalog `title` config field.

```bash
curl http://localhost:8081/catalog/ui
```

### `GET /catalog/ui/{specID}`

Serves a Redoc-powered documentation viewer for the specified OpenAPI spec. Redoc is loaded from CDN — no additional dependencies are required. The spec JSON is fetched from `/catalog/specs/{specID}`.

```bash
curl http://localhost:8081/catalog/ui/specs-users-yaml
```

Returns `404 Not Found` if the spec ID does not exist.

## Tags

Each catalog entry is tagged based on features enabled on its route. Possible tags (matching the exact strings emitted by the code):

| Tag | Condition |
|-----|-----------|
| `"gRPC"` | `grpc.enabled: true` |
| `"WebSocket"` | `websocket.enabled: true` |
| `"GraphQL"` | `graphql.enabled: true` |
| `"GraphQL Federation"` | `graphql_federation.enabled: true` |
| `"Cached"` | `cache.enabled: true` |
| `"Auth Required"` | `auth.required: true` |

Tags are appended in the order listed above. A route with multiple features enabled will have multiple tags.

## Entry Fields

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Route ID |
| `path` | string | Route path pattern |
| `methods` | string[] | Sorted list of HTTP methods (omitted if empty) |
| `description` | string | From OpenAPI spec `info.description` (omitted if empty) |
| `tags` | string[] | Feature tags (omitted if empty) |
| `backends` | int | Number of backend instances |
| `spec_id` | string | Sanitized OpenAPI spec identifier (omitted if no spec) |
| `auth` | bool | Whether authentication is required |
| `grpc` | bool | Whether gRPC is enabled (omitted if false) |
| `websocket` | bool | Whether WebSocket is enabled (omitted if false) |
| `graphql` | bool | Whether GraphQL or GraphQL Federation is enabled (omitted if false) |

## Notes

- The `spec_id` is derived from the spec file path by replacing non-alphanumeric characters with dashes (e.g., `specs/users.yaml` becomes `specs-users-yaml`).
- The `graphql` field is `true` when either `graphql.enabled` or `graphql_federation.enabled` is set on the route.
- The catalog is rebuilt from scratch on every request. There is no caching layer — this keeps it consistent with the current gateway state after reloads.
- The `backends` field is an integer count, not a list of URLs.

## See Also

- [Admin API Reference](../reference/admin-api.md#api-catalog) — Catalog endpoints in the admin API reference
- [OpenAPI Validation](../transformations/validation.md) — How OpenAPI specs are loaded via `openapi.spec_file`
- [Configuration Reference](../reference/configuration-reference.md#admin) — Catalog config fields
