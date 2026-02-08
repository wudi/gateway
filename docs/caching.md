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

## Request Coalescing (Singleflight)

When many clients request the same uncached resource simultaneously (cache stampede / thundering herd), the gateway can deduplicate these requests using request coalescing. When N identical in-flight requests arrive concurrently, only **one** goes to the backend. The other N-1 wait and share the same response.

Enable coalescing on a route:

```yaml
routes:
  - id: "api"
    path: "/api/products"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    cache:
      enabled: true
      ttl: 5m
      max_size: 1000
    coalesce:
      enabled: true
      timeout: 5s                            # max wait for coalesced requests (default 30s)
      key_headers: ["Accept", "Authorization"]  # headers included in coalesce key
      methods: ["GET", "HEAD"]               # eligible methods (default GET+HEAD)
```

### How It Works

1. A cache MISS falls through to the coalescing layer
2. The first request for a given key proceeds to the backend
3. Subsequent identical requests wait for the first to complete
4. All waiters receive the same response (and the response is cached for future requests)
5. Coalesced responses include an `X-Coalesced: true` header

### Timeout Behavior

If the primary request takes longer than `coalesce.timeout`, waiting requests fall through and call the backend independently. This prevents unbounded waiting when backends are slow.

### Coalesce Key

The coalesce key is a SHA-256 hash of: HTTP method + path + query string + configured key headers. When [GraphQL analysis](graphql.md) is enabled, the operation name and variables hash are also included. Use `key_headers` to differentiate requests that need different responses (e.g., `Authorization` for user-specific data).

### Pipeline Position

Coalescing sits between the cache and the circuit breaker:

```
... → cacheMW → coalesceMW → circuitBreakerMW → ... → proxy
```

A cache HIT returns immediately (coalescing never fires). A cache MISS enters coalescing, which deduplicates the backend call. After the coalesced response completes, the cache stores it for future requests.

### GraphQL Routes

For GraphQL routes, add `POST` to the coalesce methods:

```yaml
    coalesce:
      enabled: true
      methods: ["GET", "POST"]
```

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
| `coalesce.enabled` | bool | Enable request coalescing |
| `coalesce.timeout` | duration | Max wait for coalesced requests (default 30s) |
| `coalesce.key_headers` | []string | Headers included in coalesce key |
| `coalesce.methods` | []string | Eligible HTTP methods (default `["GET", "HEAD"]`) |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
