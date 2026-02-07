# Caching

The gateway provides an in-memory LRU response cache with per-route configuration. Cached responses bypass the backend entirely, reducing latency and load.

## Response Caching

Enable caching on a route with TTL, size limits, and method restrictions:

```yaml
routes:
  - id: "api"
    path: "/api/products"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    cache:
      enabled: true
      ttl: 5m              # cache entry time-to-live
      max_size: 1000        # max cached entries (LRU eviction)
      max_body_size: 65536  # max response body size to cache (bytes)
      methods: ["GET"]      # HTTP methods to cache
      key_headers:          # include these headers in cache key
        - "Accept"
        - "Accept-Language"
```

Cache hits return immediately with an `X-Cache: HIT` header. Cache misses proceed to the backend, and successful responses are stored with an `X-Cache: MISS` header.

## Cache Key

The default cache key is composed of: HTTP method + path + query string. You can extend it with `key_headers` to differentiate by request headers (e.g., `Accept` for content negotiation).

## GraphQL Integration

When [GraphQL analysis](graphql.md) is enabled on a route, the cache key automatically includes the GraphQL operation name and a hash of the query variables. This allows POST requests for GraphQL queries to be cached (normally only GET is cached):

```yaml
routes:
  - id: "graphql"
    path: "/graphql"
    backends:
      - url: "http://graphql-server:4000"
    graphql:
      enabled: true
    cache:
      enabled: true
      ttl: 1m
      max_size: 500
      methods: ["GET", "POST"]   # POST caching works for GraphQL queries
```

Only `query` operations are cached — `mutation` and `subscription` operations always bypass the cache.

## Cache Position in the Pipeline

The cache check happens before the circuit breaker. A cache hit never touches the backend or the circuit breaker, so cached routes remain responsive even when backends are failing.

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `cache.enabled` | bool | Enable response caching |
| `cache.ttl` | duration | Time-to-live per entry |
| `cache.max_size` | int | Max entries (LRU eviction) |
| `cache.max_body_size` | int64 | Max response body size to cache (bytes) |
| `cache.methods` | []string | HTTP methods to cache (e.g., `["GET"]`) |
| `cache.key_headers` | []string | Extra headers to include in cache key |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
