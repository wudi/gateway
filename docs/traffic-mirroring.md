# Traffic Mirroring

Traffic mirroring sends a copy of live requests to a secondary backend for testing, validation, or comparison. Mirror requests are sent asynchronously — they do not affect the primary response path.

## Basic Mirroring

Mirror a percentage of traffic to a shadow backend:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://production:9000"
    mirror:
      enabled: true
      percentage: 100       # mirror all traffic (0-100)
      backends:
        - url: "http://shadow:9000"
```

## Conditional Mirroring

Filter which requests are mirrored based on method, headers, or path pattern. All conditions use AND logic — all specified conditions must match:

```yaml
mirror:
  enabled: true
  percentage: 50
  backends:
    - url: "http://shadow:9000"
  conditions:
    methods: ["GET", "POST"]             # only mirror these methods
    headers:
      X-Mirror: "true"                   # only if this header is present
    path_regex: "^/api/v2/.*"            # only matching paths
```

Conditions are checked before the percentage roll — a request must match all conditions, then pass the percentage check, to be mirrored.

## Response Comparison

Compare the primary and mirror responses to detect behavioral differences:

```yaml
mirror:
  enabled: true
  percentage: 100
  backends:
    - url: "http://shadow:9000"
  compare:
    enabled: true
    log_mismatches: true     # log when responses differ
```

Comparison checks:
- **Status code**: exact match between primary and mirror
- **Response body**: SHA-256 hash comparison (streaming, no full body buffering)

When `log_mismatches` is enabled, differences are logged with both status codes and body hash match status.

## Mirror Metrics

The mirror tracks per-route metrics accessible via the [Admin API](admin-api.md) at `/mirrors`:

- **Total mirrored requests** — count of requests sent to mirror backends
- **Success/failure counts** — mirror backend response outcomes
- **Latency percentiles** — mirror response time distribution (ring buffer of 1000 samples)
- **Comparison results** — match/mismatch counts when comparison is enabled

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `mirror.enabled` | bool | Enable traffic mirroring |
| `mirror.percentage` | int | Percentage of traffic to mirror (0-100) |
| `mirror.backends` | []BackendConfig | Mirror target backends |
| `mirror.conditions.methods` | []string | HTTP methods to mirror |
| `mirror.conditions.headers` | map | Required headers |
| `mirror.conditions.path_regex` | string | Path regex filter |
| `mirror.compare.enabled` | bool | Enable response comparison |
| `mirror.compare.log_mismatches` | bool | Log response differences |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
