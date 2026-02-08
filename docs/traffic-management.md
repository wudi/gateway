# Traffic Management

Traffic management features allow you to split traffic between backend groups for A/B testing, canary deployments, and gradual rollouts.

## Traffic Splits

Define weighted groups of backends. Each request is assigned to a group based on weight distribution. Weights must sum to 100.

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    traffic_split:
      - name: "stable"
        weight: 90
        backends:
          - url: "http://v1-backend:9000"
      - name: "canary"
        weight: 10
        backends:
          - url: "http://v2-backend:9000"
```

The assigned group name is sent to the client as an `X-AB-Variant` response header.

### Header-Based Override

Each traffic split group can define `match_headers` for deterministic routing. If a request matches a group's headers, it goes to that group regardless of weight:

```yaml
traffic_split:
  - name: "stable"
    weight: 90
    backends:
      - url: "http://v1-backend:9000"
  - name: "canary"
    weight: 10
    backends:
      - url: "http://v2-backend:9000"
    match_headers:
      X-Canary: "true"    # requests with this header always go to canary
```

## Sticky Sessions

Sticky sessions ensure a client consistently reaches the same traffic group across requests. Three modes are available:

### Cookie Mode

Sets a cookie on the first response, then reads it on subsequent requests:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    traffic_split:
      - name: "stable"
        weight: 90
        backends:
          - url: "http://v1:9000"
      - name: "canary"
        weight: 10
        backends:
          - url: "http://v2:9000"
    sticky:
      enabled: true
      mode: "cookie"
      cookie_name: "X-Traffic-Group"   # default
      ttl: 24h                         # default
```

### Header Mode

Determines group by hashing a request header value (FNV-32a). Returns empty if header absent, falling back to weighted random:

```yaml
sticky:
  enabled: true
  mode: "header"
  hash_key: "X-User-ID"     # required
```

### Hash Mode

Hashes a header value (or client IP as fallback) for deterministic group assignment:

```yaml
sticky:
  enabled: true
  mode: "hash"
  hash_key: "X-Session-ID"  # required, falls back to client IP if absent
```

## Canary Deployments

The gateway supports automated canary deployments that progressively shift traffic to a canary group while monitoring error rate and p99 latency. If health thresholds are breached, traffic is automatically rolled back.

```yaml
routes:
  - id: api
    path: /api
    path_prefix: true
    traffic_split:
      - name: stable
        weight: 95
        backends:
          - url: http://v1:8080
      - name: canary
        weight: 5
        backends:
          - url: http://v2:8080
    canary:
      enabled: true
      canary_group: canary
      steps:
        - weight: 5
          pause: 5m
        - weight: 25
          pause: 10m
        - weight: 50
          pause: 15m
        - weight: 100
      analysis:
        error_threshold: 0.05
        latency_threshold: 500ms
        min_requests: 100
        interval: 30s
```

Canary deployments are started and controlled via the admin API (`POST /canary/{route}/start`). See [Canary Deployments](canary-deployments.md) for full documentation including the state machine, weight redistribution, and admin API.

## Constraints

- Traffic splits require weights summing to 100
- Sticky sessions require `traffic_split` to be configured
- `hash_key` is required for `header` and `hash` modes
- Advanced load balancers (`least_conn`, `consistent_hash`, `least_response_time`) are incompatible with traffic splits

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `traffic_split[].name` | string | Group name (appears in `X-AB-Variant` header) |
| `traffic_split[].weight` | int | Traffic percentage (0-100, all must sum to 100) |
| `traffic_split[].match_headers` | map | Header overrides for deterministic routing |
| `sticky.mode` | string | `cookie`, `header`, or `hash` |
| `sticky.cookie_name` | string | Cookie name (default `X-Traffic-Group`) |
| `sticky.hash_key` | string | Header name for header/hash modes |
| `sticky.ttl` | duration | Cookie TTL (default 24h) |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
