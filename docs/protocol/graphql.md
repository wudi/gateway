---
title: "GraphQL Protection"
sidebar_position: 2
---

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

## Automatic Persisted Queries (APQ)

APQ reduces bandwidth by allowing clients to send a hash of a previously registered query instead of the full query text. This follows the [Apollo APQ protocol](https://www.apollographql.com/docs/apollo-server/performance/apq/).

```yaml
graphql:
  enabled: true
  persisted_queries:
    enabled: true
    max_size: 1000    # LRU cache max entries (default 1000)
```

### How it works

1. **First request (register):** Client sends both the query and its SHA-256 hash in the `extensions` field. The gateway verifies the hash matches, stores it in the LRU cache, and forwards the request.

2. **Subsequent requests (lookup):** Client sends only the hash (no query). The gateway looks up the hash in the cache, substitutes the full query, and forwards it to the backend.

3. **Cache miss:** If the hash is not found, the gateway returns a `PersistedQueryNotFound` error (HTTP 200, per Apollo protocol). The client should retry with the full query + hash.

### Request format

```json
{
  "extensions": {
    "persistedQuery": {
      "version": 1,
      "sha256Hash": "ecf4edb46db40b5132295c0291d62fb65d6759a9eedfa4d5d612dd5ec54a6b38"
    }
  }
}
```

### Security

- The gateway verifies that the SHA-256 hash matches the query text before storing, preventing cache poisoning.
- The LRU cache evicts least recently used queries when full.

## Query Batching

GraphQL clients (Apollo, Relay, urql) can batch multiple operations into a single HTTP request by sending a JSON array instead of a single object. The gateway detects batched requests and validates each query individually.

```yaml
graphql:
  enabled: true
  batching:
    enabled: true
    max_batch_size: 10    # max queries per batch (default 10, 0 = unlimited)
    mode: "pass_through"  # "pass_through" or "split" (default "pass_through")
```

### Batch detection

A request body starting with `[` is treated as a batch. Each element must be a standard GraphQL request object (`{query, variables, operationName, extensions}`). If batching is not enabled and an array is received, the gateway returns a 400 error.

### Per-query validation

Every query in a batch is individually validated against depth limits, complexity limits, introspection control, and per-operation rate limits. If any query fails validation, the entire batch is rejected with an error referencing the query index (e.g., `"query[2]: depth 15 exceeds maximum 10"`).

APQ (Automatic Persisted Queries) resolution also works per-query within a batch — each element can use hash-only lookups independently.

### Execution modes

**Pass-through mode** (`mode: "pass_through"`, default): The gateway validates all queries, resolves any APQ hashes, then forwards the entire JSON array to the backend. Use this when the backend natively supports batch requests.

**Split mode** (`mode: "split"`): The gateway fans out each query as an individual HTTP request through the full downstream middleware chain (cache, circuit breaker, etc.), then merges the responses into a JSON array. Use this when the backend only handles single queries, or when you want each query to benefit from per-query caching and circuit breaking.

### Empty batches

An empty array `[]` returns an empty array response `[]` with status 200.

### Metrics

Batch metrics are exposed via the admin `/graphql` endpoint:

- `batching.requests_total` — number of batch requests received
- `batching.queries_total` — total individual queries across all batches
- `batching.size_rejected` — batches rejected for exceeding `max_batch_size`

## Cache Integration

When used with [caching](../caching/caching.md), GraphQL analysis enhances cache keys with the operation name and a hash of query variables. This enables caching of GraphQL POST requests for query operations (mutations and subscriptions always bypass cache).

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
| `graphql.persisted_queries.enabled` | bool | Enable Automatic Persisted Queries |
| `graphql.persisted_queries.max_size` | int | LRU cache max entries (default 1000) |
| `graphql.batching.enabled` | bool | Enable query batching |
| `graphql.batching.max_batch_size` | int | Max queries per batch (default 10, 0 = unlimited) |
| `graphql.batching.mode` | string | `"pass_through"` or `"split"` (default `"pass_through"`) |

See [Configuration Reference](../reference/configuration-reference.md#routes) for all fields.
