# GraphQL Protection

The gateway can analyze GraphQL queries in transit to enforce depth limits, complexity limits, introspection control, and per-operation-type rate limits. This protects GraphQL backends from abusive or excessively expensive queries.

## Enabling GraphQL Analysis

Enable GraphQL on a route that fronts a GraphQL backend:

```yaml
routes:
  - id: "graphql"
    path: "/graphql"
    backends:
      - url: "http://graphql-server:4000"
    graphql:
      enabled: true
      max_depth: 10
      max_complexity: 100
      introspection: false
      operation_limits:
        query: 100         # queries per second
        mutation: 10        # mutations per second
        subscription: 5     # subscriptions per second
```

GraphQL analysis only activates for `POST` requests with `Content-Type: application/json`. Other requests pass through unchanged.

## Query Depth Limiting

Prevents deeply nested queries that can cause exponential backend work:

```yaml
graphql:
  enabled: true
  max_depth: 10    # 0 = unlimited
```

A query like `{ user { posts { comments { author { posts { ... } } } } } }` has depth 5. Queries exceeding `max_depth` are rejected with a GraphQL error response.

## Query Complexity Limiting

Limits the total complexity score of a query. Each field selection adds 1 to the complexity count:

```yaml
graphql:
  enabled: true
  max_complexity: 200    # 0 = unlimited
```

## Introspection Control

Block introspection queries (`__schema`, `__type`) in production:

```yaml
graphql:
  enabled: true
  introspection: false    # default: false
```

When disabled, introspection queries are rejected with a GraphQL error response.

## Per-Operation Rate Limits

Rate limit by GraphQL operation type (query, mutation, subscription):

```yaml
graphql:
  enabled: true
  operation_limits:
    query: 100        # max queries per second
    mutation: 10      # max mutations per second
    subscription: 5   # max subscriptions per second
```

Each operation type has its own independent token bucket. Exceeded operations return a GraphQL error response.

## Cache Integration

When used with [caching](caching.md), GraphQL analysis enhances cache keys with the operation name and a hash of query variables. This enables caching of GraphQL POST requests for query operations (mutations and subscriptions always bypass cache).

## Error Responses

All GraphQL errors are returned in the standard GraphQL JSON format:

```json
{
  "errors": [
    {
      "message": "query depth 15 exceeds maximum allowed depth of 10"
    }
  ]
}
```

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `graphql.enabled` | bool | Enable GraphQL analysis |
| `graphql.max_depth` | int | Max query nesting depth (0 = unlimited) |
| `graphql.max_complexity` | int | Max query complexity score (0 = unlimited) |
| `graphql.introspection` | bool | Allow introspection queries (default false) |
| `graphql.operation_limits` | map | Per-type rate limits: `query`, `mutation`, `subscription` |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
