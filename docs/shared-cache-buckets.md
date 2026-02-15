# Shared Cache Buckets

Multiple routes can share the same cache store by specifying a named `bucket`. Routes with the same bucket share cache entries, enabling cross-route cache reuse.

## Configuration

```yaml
routes:
  - id: products-v1
    path: /api/v1/products
    backends:
      - url: http://backend:8080
    cache:
      enabled: true
      ttl: 5m
      bucket: product-cache

  - id: products-v2
    path: /api/v2/products
    backends:
      - url: http://backend:8080
    cache:
      enabled: true
      ttl: 5m
      bucket: product-cache
```

Both routes share cache entries because they use the same `product-cache` bucket. A cache entry stored via `/api/v1/products` is retrievable via `/api/v2/products` if the cache key matches (same method, path, query, and key headers).

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `bucket` | string | (empty) | Named shared cache bucket. Routes with the same name share a store. |

When `bucket` is empty (default), each route uses its own isolated cache store — this is the existing behavior.

## Bucket Name Rules

Bucket names must contain only alphanumeric characters, hyphens, and underscores (e.g., `product-cache`, `user_data`).

## Distributed Mode

When using `mode: distributed` (Redis-backed cache), shared bucket routes use the Redis key prefix `gw:cache:bucket:<bucket_name>:` instead of the per-route prefix `gw:cache:<route_id>:`.

```yaml
redis:
  address: localhost:6379

routes:
  - id: api-v1
    path: /v1/data
    cache:
      enabled: true
      mode: distributed
      bucket: shared-api
```

## Admin API

The `GET /cache` endpoint shows bucket membership in the stats:

```json
{
  "products-v1": {
    "size": 42,
    "max_size": 1000,
    "hits": 150,
    "misses": 30,
    "bucket": "product-cache"
  },
  "products-v2": {
    "size": 42,
    "max_size": 1000,
    "hits": 85,
    "misses": 15,
    "bucket": "product-cache"
  }
}
```

Note that `size` is the same for routes sharing a bucket (they read from the same store), but `hits` and `misses` are tracked per-handler.
