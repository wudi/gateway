---
title: "Caching"
sidebar_position: 1
---

The gateway provides per-route response caching with two storage backends: in-memory LRU (default) and Redis for distributed multi-instance deployments. Cached responses bypass the backend entirely, reducing latency and load.

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

When [GraphQL analysis](../protocol/graphql.md) is enabled on a route, the cache key automatically includes the GraphQL operation name and a hash of the query variables. This allows POST requests for GraphQL queries to be cached (normally only GET is cached):

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

## Conditional Caching (ETags / 304 Not Modified)

When `conditional: true` is set on a cache-enabled route, the gateway supports HTTP conditional requests. This allows clients that already have a cached copy to validate it with lightweight `304 Not Modified` responses instead of re-downloading the full body.

```yaml
routes:
  - id: "api"
    path: "/api/products"
    backends:
      - url: "http://backend:9000"
    cache:
      enabled: true
      conditional: true    # enable ETag/Last-Modified/304
      ttl: 5m
      max_size: 1000
```

### How It Works

1. When a response is cached, the gateway generates an `ETag` (SHA-256 hash of the body) and records a `Last-Modified` timestamp. If the backend already provides these headers, the backend values are used instead.
2. On a cache HIT, the gateway checks the request for conditional headers:
   - `If-None-Match` — compared against the cached `ETag` (supports `*` and comma-separated lists)
   - `If-Modified-Since` — compared against the cached `Last-Modified` timestamp
3. If the client's cached version is still fresh, the gateway returns `304 Not Modified` with no body.
4. If the content has changed (or no conditional headers are present), the full `200` response is returned as usual.

`If-None-Match` takes precedence over `If-Modified-Since` per RFC 7232.

### Response Headers

On cache HITs with `conditional: true`, the following headers are always included:

| Header | Description |
|--------|-------------|
| `ETag` | Strong ETag of the cached response body |
| `Last-Modified` | Timestamp when the entry was cached (or backend value) |
| `X-Cache` | `HIT` (present on all cache hits) |

### Metrics

304 responses are tracked in:
- The per-route `CacheStats.not_modifieds` counter (visible at `GET /cache`)
- The `gateway_cache_not_modified_total` Prometheus counter (with `route` label)

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

The coalesce key is a SHA-256 hash of: HTTP method + path + query string + configured key headers. When [GraphQL analysis](../protocol/graphql.md) is enabled, the operation name and variables hash are also included. Use `key_headers` to differentiate requests that need different responses (e.g., `Authorization` for user-specific data).

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

## Distributed Caching (Redis)

In multi-instance deployments, each gateway instance maintains its own in-memory cache by default, leading to duplicate backend requests and inconsistent cache state. Enable distributed caching to share cached responses across all instances via Redis:

```yaml
redis:
  address: "localhost:6379"
  pool_size: 10

routes:
  - id: "api"
    path: "/api/products"
    backends:
      - url: "http://backend:9000"
    cache:
      enabled: true
      mode: "distributed"      # use Redis backend (default: "local")
      ttl: 5m
      max_body_size: 65536
      methods: ["GET"]
      key_headers: ["Accept"]
```

### How It Works

- **`mode: "local"`** (default): In-memory LRU cache per instance. Fast, no external dependency, but not shared.
- **`mode: "distributed"`**: Redis-backed cache shared across all gateway instances. Requires `redis.address` to be configured.

Redis keys use the prefix `gw:cache:{routeID}:` followed by the cache key hash. TTL is enforced by Redis key expiration.

### Fail-Open Behavior

Redis errors (timeouts, connection failures) are treated as cache misses — the request proceeds to the backend normally. A warning is logged but requests are never blocked by Redis unavailability. Each Redis operation has a 100ms timeout to prevent latency cascading.

### Admin API

The `GET /cache` endpoint shows per-route statistics. For distributed mode, `size` reflects the number of keys in Redis for that route's prefix. `hits` and `misses` are counted locally per instance. `max_size` and `evictions` are 0 for distributed mode (Redis manages eviction via TTL).

### Notes

- `max_size` is ignored for distributed mode (Redis manages memory via TTL expiration).
- The gateway reuses the shared Redis client configured under `redis:` (same as distributed rate limiting).
- Responses are serialized using `encoding/gob`.

## Stale-While-Revalidate

When `stale_while_revalidate` is set, the gateway serves stale cached responses immediately while refreshing the entry in the background. This eliminates latency spikes when cache entries expire, because clients always get an instant response.

```yaml
routes:
  - id: "api"
    path: "/api/products"
    backends:
      - url: "http://backend:9000"
    cache:
      enabled: true
      ttl: 1m
      stale_while_revalidate: 30s   # serve stale for up to 30s while refreshing
      max_size: 1000
```

### How It Works

1. During the `ttl` window, the entry is **fresh** and served normally with `X-Cache: HIT`.
2. After `ttl` expires but within `ttl + stale_while_revalidate`, the entry is **stale**. The gateway:
   - Serves the stale entry immediately with `X-Cache: STALE`
   - Launches a background goroutine to fetch a fresh response from the backend
   - Stores the fresh response for subsequent requests
3. Background revalidations are deduplicated per cache key -- concurrent requests for the same stale key share a single backend call.
4. After `ttl + stale_while_revalidate`, the entry is fully expired and treated as a cache miss.

## Stale-If-Error

When `stale_if_error` is set, the gateway falls back to a stale cached response if the backend returns a 5xx error. This improves availability by shielding clients from backend failures.

```yaml
routes:
  - id: "api"
    path: "/api/products"
    backends:
      - url: "http://backend:9000"
    cache:
      enabled: true
      ttl: 1m
      stale_if_error: 5m            # serve stale for up to 5m on backend errors
      max_size: 1000
```

### How It Works

1. During the `ttl` window, the entry is fresh and served normally.
2. On a cache miss or stale entry, the request proceeds to the backend.
3. If the backend returns a 5xx status code and a stale entry exists within `ttl + stale_if_error`, the stale entry is served with `X-Cache: STALE` instead of the error response.
4. If the backend succeeds, the fresh response is served and cached normally.

### Combining Both

You can use `stale_while_revalidate` and `stale_if_error` together:

```yaml
    cache:
      enabled: true
      ttl: 1m
      stale_while_revalidate: 30s   # instant responses during refresh
      stale_if_error: 10m           # resilience against backend failures
```

The stale window extends to `ttl + max(stale_while_revalidate, stale_if_error)`. Within this window:
- If the entry age is within `ttl + stale_while_revalidate`, the stale entry is served immediately with background refresh.
- If the entry age is beyond the SWR window but within `ttl + stale_if_error`, the backend is called normally, but a stale fallback is used if the backend returns 5xx.

### Response Headers

| `X-Cache` Value | Meaning |
|-----------------|---------|
| `HIT` | Fresh cache hit |
| `STALE` | Stale entry served (SWR or SIE fallback) |
| `MISS` | Cache miss, response from backend |

## Cache Position in the Pipeline

The cache check happens before the circuit breaker. A cache hit never touches the backend or the circuit breaker, so cached routes remain responsive even when backends are failing.

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `cache.enabled` | bool | Enable response caching |
| `cache.mode` | string | `"local"` (default) or `"distributed"` (Redis-backed) |
| `cache.conditional` | bool | Enable ETag/Last-Modified/304 Not Modified support |
| `cache.ttl` | duration | Time-to-live per entry |
| `cache.max_size` | int | Max entries (LRU eviction, local mode only) |
| `cache.max_body_size` | int64 | Max response body size to cache (bytes) |
| `cache.methods` | []string | HTTP methods to cache (e.g., `["GET"]`) |
| `cache.key_headers` | []string | Extra headers to include in cache key |
| `cache.stale_while_revalidate` | duration | Serve stale while refreshing in background |
| `cache.stale_if_error` | duration | Serve stale on backend 5xx errors |
| `cache.tag_headers` | []string | Response headers to extract cache tags from (split on space/comma) |
| `cache.tags` | []string | Static tags applied to all cached entries on this route |
| `coalesce.enabled` | bool | Enable request coalescing |
| `coalesce.timeout` | duration | Max wait for coalesced requests (default 30s) |
| `coalesce.key_headers` | []string | Headers included in coalesce key |
| `coalesce.methods` | []string | Eligible HTTP methods (default `["GET", "HEAD"]`) |

See [Configuration Reference](../reference/configuration-reference.md#routes) for all fields.

## Cache Invalidation API

The gateway provides an admin API endpoint for programmatic cache purging. This allows you to invalidate cached responses without waiting for TTL expiration — useful when backend data changes and stale responses must be evicted immediately.

### POST `/cache/purge`

Purge cached entries by route, by specific key, or globally.

#### Purge all entries for a route

```bash
curl -X POST http://localhost:8081/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"route": "my-route"}'
```

**Response (200 OK):**
```json
{
  "purged": true,
  "entries_removed": 42
}
```

This removes every cached entry belonging to the specified route. For local mode, this clears the in-memory LRU for that route. For distributed mode, this deletes all Redis keys under the `gw:cache:{routeID}:` prefix.

#### Purge a specific cache key

```bash
curl -X POST http://localhost:8081/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"route": "my-route", "key": "/api/products?category=shoes"}'
```

**Response (200 OK):**
```json
{
  "purged": true,
  "entries_removed": 1
}
```

The `key` value should match the request path and query string of the cached response. The cache key is computed from the method + path + query string (plus any `key_headers`), so the `key` field corresponds to the path+query portion. If the key does not exist, `entries_removed` is `0` and `purged` is still `true`.

#### Purge all caches globally

```bash
curl -X POST http://localhost:8081/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"all": true}'
```

**Response (200 OK):**
```json
{
  "purged": true,
  "entries_removed": 1500
}
```

This clears every cached entry across all routes.

#### Purge by cache tags

```bash
curl -X POST http://localhost:8081/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"route": "products", "tags": ["product", "listing"]}'
```

**Response (200 OK):**
```json
{
  "purged": true,
  "entries_removed": 15
}
```

Removes all cached entries on the route that are tagged with any of the specified tags. Tags are collected from two sources:

- **Static tags** — configured via `cache.tags` on the route, applied to every cached entry.
- **Header tags** — extracted from response headers listed in `cache.tag_headers`. Header values are split on spaces and commas.

```yaml
routes:
  - id: products
    path: /api/products
    path_prefix: true
    backends:
      - url: http://product-service:9000
    cache:
      enabled: true
      ttl: 10m
      tag_headers:
        - Cache-Tag
        - Surrogate-Key
      tags:
        - products
```

When the backend returns `Cache-Tag: product listing`, the entry is tagged with `["products", "product", "listing"]` (static tag + header tags).

#### Purge by path pattern

```bash
curl -X POST http://localhost:8081/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"route": "products", "path_pattern": "/api/products/*"}'
```

**Response (200 OK):**
```json
{
  "purged": true,
  "entries_removed": 8
}
```

Removes all cached entries on the route whose original request path matches the given glob pattern. Uses Go's `path.Match` syntax (supports `*` and `?` wildcards).

#### Error responses

**400 Bad Request** — invalid JSON or missing required fields:
```json
{
  "error": "must specify 'route', 'route'+'key', or 'all'"
}
```

**404 Not Found** — specified route does not have caching enabled:
```json
{
  "error": "route 'unknown-route' not found or caching not enabled"
}
```

### Example: Purge on deploy

Integrate cache purging into your deployment pipeline to ensure users see fresh content after a release:

```bash
#!/bin/bash
# After deploying new backend version
curl -s -X POST http://gateway:8081/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"all": true}' | jq .
```

### Example: Targeted invalidation via webhook

Combine with [webhooks](../observability/webhooks.md) to trigger cache purges when specific resources change:

```yaml
routes:
  - id: "products"
    path: "/api/products"
    path_prefix: true
    backends:
      - url: "http://product-service:9000"
    cache:
      enabled: true
      ttl: 10m
      max_size: 5000
      methods: ["GET"]
```

```bash
# When product ID 123 is updated, purge its cache entry
curl -X POST http://gateway:8081/cache/purge \
  -H "Content-Type: application/json" \
  -d '{"route": "products", "key": "/api/products/123"}'
```
