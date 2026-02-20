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

## Blue-Green Deployments

Blue-green provides binary all-or-nothing traffic cutover between two backend groups, unlike canary's gradual weight-shifting approach.

```yaml
routes:
  - id: api
    traffic_split:
      - name: blue
        weight: 100
        backends:
          - url: http://blue-v1:8080
      - name: green
        weight: 0
        backends:
          - url: http://green-v2:8080
    blue_green:
      enabled: true
      active_group: blue
      inactive_group: green
      rollback_on_error: true
      error_threshold: 0.05
      observation_window: 60s
      min_requests: 100
```

### State Machine

`inactive` -> `promoting` -> `active` / `rolled_back`

### Operations

- **Promote** (`POST /blue-green/{route}/promote`): Switches all traffic to the inactive group (100% weight). Starts observation window.
- **Rollback** (`POST /blue-green/{route}/rollback`): Restores original weights. Available from promoting or active states.
- **Status** (`GET /blue-green/{route}/status`): Returns current state, group metrics, error rates.

### Auto-Rollback

When `rollback_on_error: true`, the observation goroutine monitors error rate on the promoted group. If errors exceed `error_threshold` after `min_requests` are received, traffic is automatically rolled back to original weights.

### Canary vs Blue-Green

| Feature | Canary | Blue-Green |
|---------|--------|------------|
| Traffic shift | Gradual steps | All-or-nothing |
| Rollback | Restores to step 0 | Restores original weights |
| Observation | Per-step | Single window after promote |
| Mutually exclusive | Yes | Yes |

**Validation:** Blue-green and canary are mutually exclusive on the same route. Both require `traffic_split`.

See [Blue-Green Deployments](blue-green.md) for full documentation.

---

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
