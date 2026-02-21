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

## Shadow Testing / Detailed Diff Mode

For deeper insight into what differs between primary and mirror responses, enable `detailed_diff`. This extends comparison with field-level body diffs, header diffs, and a mismatch store for admin API inspection.

```yaml
mirror:
  enabled: true
  percentage: 100
  backends:
    - url: "http://shadow-v2:9000"
  compare:
    enabled: true
    log_mismatches: true
    detailed_diff: true
    max_body_capture: 1048576    # 1 MiB (default)
    max_mismatches: 100          # ring buffer capacity (default)
    ignore_headers:
      - "Server"
      - "X-Request-Duration"
    ignore_json_fields:
      - "timestamp"
      - "request_id"
```

When `detailed_diff` is enabled:

- **Status code**: exact comparison with both values reported on mismatch
- **Headers**: compares all response headers between primary and mirror, skipping `Date`, `X-Request-Id`, and any headers in `ignore_headers`
- **Body (JSON)**: if both responses are valid JSON, compares field-by-field using [gjson](https://github.com/tidwall/gjson) paths, skipping fields in `ignore_json_fields`
- **Body (non-JSON)**: reports content diff with previews
- **Body (truncated)**: if either body exceeds `max_body_capture`, falls back to SHA-256 hash comparison

Mismatches are stored in a per-route ring buffer (capacity set by `max_mismatches`) and can be inspected via the admin API.

### Mismatch Admin API

Inspect recent mismatches:

```
GET /mirrors/{routeID}/mismatches
```

Returns:
```json
{
  "total_mismatches": 47,
  "stored_count": 10,
  "capacity": 100,
  "entries": [
    {
      "timestamp": "2026-02-21T10:30:00Z",
      "method": "GET",
      "path": "/api/users/123",
      "backend": "http://shadow-v2:9000",
      "detail": {
        "status_diff": null,
        "header_diffs": [],
        "body_diffs": [
          {
            "type": "json_field_diff",
            "details": {
              "field": "name",
              "primary_value": "\"Alice\"",
              "mirror_value": "\"alice\""
            }
          }
        ]
      },
      "diff_types": ["body"]
    }
  ]
}
```

Clear stored mismatches:

```
DELETE /mirrors/{routeID}/mismatches
```

Returns `{"status":"cleared"}`.

### Detailed Diff Metrics

When detailed diff is enabled, the `/mirrors` endpoint includes per-type mismatch counters:

- `status_mismatches` — count of responses with different status codes
- `header_mismatches` — count of responses with different headers
- `body_mismatches` — count of responses with different body content
- `mismatch_store_size` — current number of entries in the ring buffer

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
| `mirror.compare.detailed_diff` | bool | Enable field-level diff (requires `enabled`) |
| `mirror.compare.max_body_capture` | int | Max bytes to buffer for diff (default 1 MiB) |
| `mirror.compare.max_mismatches` | int | Ring buffer capacity (default 100) |
| `mirror.compare.ignore_headers` | []string | Headers to exclude from comparison |
| `mirror.compare.ignore_json_fields` | []string | gjson paths to ignore in JSON body diff |

See [Configuration Reference](configuration-reference.md#routes) for all fields.
