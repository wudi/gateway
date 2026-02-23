# GraphQL Federation (Schema Stitching)

The gateway can merge multiple backend GraphQL schemas into a unified schema, routing queries to the backend that owns each root field. This provides a single GraphQL endpoint for clients while keeping backend services independent.

## Configuration

```yaml
routes:
  - id: unified-graphql
    path: /graphql
    graphql_federation:
      enabled: true
      refresh_interval: 5m
      sources:
        - name: users
          url: "http://users:4000/graphql"
        - name: orders
          url: "http://orders:4001/graphql"
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable GraphQL federation for this route |
| `refresh_interval` | duration | `5m` | How often to re-introspect backend schemas |
| `sources[].name` | string | required | Unique name for the backend source |
| `sources[].url` | string | required | GraphQL endpoint URL for the backend |

## How It Works

1. **Schema Introspection**: At startup, the gateway sends an introspection query to each backend source and retrieves its schema.

2. **Schema Merging**: Root Query, Mutation, and Subscription fields from all sources are merged into a single schema. Each root field is tracked by its owning source. Non-root types use first-seen-wins for conflicts.

3. **Query Splitting**: When a client query arrives, the gateway parses it and groups top-level fields by their owning source. If all fields belong to one source, the original query is forwarded as-is.

4. **Fan-out Execution**: For cross-backend queries, the gateway builds per-source sub-queries and executes them concurrently.

5. **Response Merging**: Data and errors from all backends are merged into a single GraphQL response.

## Example

Given two backends:

**Users service** (`users`):
```graphql
type Query {
  users: [User!]!
  user(id: ID!): User
}
```

**Orders service** (`orders`):
```graphql
type Query {
  orders: [Order!]!
  order(id: ID!): Order
}
type Mutation {
  createOrder(input: OrderInput!): Order
}
```

A client can query both in a single request:

```graphql
{
  users { id name }
  orders { id total }
}
```

The gateway splits this into two sub-queries, sends `{ users { id name } }` to the users service and `{ orders { id total } }` to the orders service, then merges the responses.

## Introspection

The merged schema is served for introspection queries. Clients using schema-aware tools (GraphiQL, Apollo DevTools) will see the combined schema with all fields from all sources.

## Schema Refresh

Schemas are periodically re-introspected based on `refresh_interval`. This allows the gateway to pick up schema changes from backends without restarts. If a refresh fails, the previously cached schema continues to be used.

## Conflict Detection

If two sources define the same root field (e.g., both have `Query.users`), schema merging fails with a conflict error. Each root field must be owned by exactly one source.

## Limitations

- **Root-level stitching only**: Fields are routed at the top level of Query/Mutation/Subscription. Cross-service entity resolution (like Apollo Federation's `@key`/`@requires`) is not supported.
- **No query planning**: Nested field dependencies across services are not resolved. Each top-level field is sent entirely to its owning source.
- **Variables forwarded to all sub-queries**: When a query is split, all variables are sent to each sub-query. Unused variables are ignored by GraphQL backends.

## HTTP Request/Response Example

Send a cross-backend query through the federated endpoint:

```bash
curl -X POST http://localhost:8080/graphql \
  -H "Content-Type: application/json" \
  -d '{"query":"{ users { id name } orders { id total } }"}'
```

**Response:**
```json
{
  "data": {
    "users": [
      {"id": "1", "name": "Alice"},
      {"id": "2", "name": "Bob"}
    ],
    "orders": [
      {"id": "101", "total": 59.99},
      {"id": "102", "total": 120.00}
    ]
  }
}
```

The gateway split this into `{ users { id name } }` sent to the users source and `{ orders { id total } }` sent to the orders source, then merged the two responses.

## Admin API

```bash
curl http://localhost:8081/graphql-federation
```

Returns per-route federation statistics:

```json
{
  "unified-graphql": {
    "sources": 2,
    "requests": 5000,
    "errors": 12,
    "introspections": 45,
    "refresh_interval": "5m0s",
    "fields": 5
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `sources` | int | Number of configured federation sources |
| `requests` | int | Total queries handled |
| `errors` | int | Total query errors |
| `introspections` | int | Total introspection queries served |
| `refresh_interval` | string | Schema refresh interval |
| `fields` | int | Number of root fields in the merged schema |

## Error Handling

- **Init failure**: During startup, if fewer than 2 sources are reachable, `Init()` returns an error and the route fails to set up. Unreachable sources are logged as warnings and skipped — only the count of successful sources matters.
- **Refresh failure**: When a periodic refresh fails (e.g., a source is temporarily down), the gateway logs a warning and continues using the previously merged schema. The stale schema remains in use until a subsequent refresh succeeds.
- **Partial source unavailability**: If some sources fail introspection during a refresh but at least 2 succeed, the schema is re-merged from the available sources. Fields from unavailable sources are dropped until they recover.
- **Query errors**: If a sub-query to a backend fails during fan-out execution, the error is included in the GraphQL `errors` array and the `errors` counter is incremented.

## Validation Rules

- Requires at least 2 sources
- Source names must be unique within a route
- Each source must have a URL
- Mutually exclusive with `graphql.enabled` (depth/complexity limiting)
- Mutually exclusive with `protocol` translation
- Federation routes do not require `backends` (sources provide their own URLs)

## Notes

- The `fields` stat in the admin API counts the total number of root-level fields across all merged types (Query, Mutation, Subscription).
- Introspection queries (`__schema`, `__type`) are served from the merged schema and do not fan out to backends.
- The `refresh_interval` default is `5m` (5 minutes).

## See Also

- [GraphQL Protection](graphql.md) — Depth/complexity limiting (mutually exclusive with federation)
- [Admin API Reference](admin-api.md#graphql-federation) — Federation stats endpoint
- [Configuration Reference](configuration-reference.md) — Full config schema
- [Examples](examples.md#graphql-federation) — Complete working config
