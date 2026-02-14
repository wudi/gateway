# Response Aggregation

Response aggregation enables parallel multi-backend calls with JSON response merging. This is useful for composing responses from multiple microservices into a single API response.

## Configuration

Aggregate is configured per-route using the `aggregate` field:

```yaml
routes:
  - id: user-profile
    path: /users/:id/profile
    aggregate:
      enabled: true
      timeout: 5s            # global timeout for all backends (default 5s)
      fail_strategy: partial # "abort" (default) or "partial"
      backends:
        - name: user
          url: "http://user-service/users/{{index .PathParams \"id\"}}"
          group: user         # wrap response under "user" key
          required: true      # abort even in partial mode if this fails
        - name: orders
          url: "http://order-service/orders?user={{index .PathParams \"id\"}}"
          group: orders
        - name: preferences
          url: "http://pref-service/prefs/{{index .PathParams \"id\"}}"
          method: GET         # default GET
          group: preferences
          timeout: 2s         # per-backend timeout override
          headers:
            X-Internal: "true"
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable response aggregation |
| `timeout` | duration | 5s | Global timeout for all backend calls |
| `fail_strategy` | string | "abort" | How to handle backend failures: `abort` or `partial` |
| `backends` | list | required | List of backends to call in parallel |

### Backend Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | required | Unique backend name |
| `url` | string | required | URL template (Go `text/template`) |
| `method` | string | GET | HTTP method |
| `headers` | map | - | Header templates |
| `group` | string | - | Wrap response under this JSON key |
| `required` | bool | false | Abort if this backend fails (even in partial mode) |
| `timeout` | duration | global | Per-backend timeout override |

## URL Templates

Backend URLs support Go `text/template` with the following context:

- `.Method` — original request method
- `.Path` — original request path
- `.Host` — original request host
- `.PathParams` — path parameters (e.g., `{{index .PathParams "id"}}`)
- `.Query` — query parameters
- `.Headers` — request headers
- `.ClientIP` — client IP address
- `.RouteID` — route ID

Template functions: `json` (marshal to JSON string).

## Fail Strategies

### abort (default)

Returns 502 Bad Gateway if **any** backend fails. The response includes error details:

```json
{
  "error": "aggregate backend failure",
  "errors": [{"backend": "orders", "error": "HTTP 500"}]
}
```

### partial

Returns successful responses and includes `_errors` array for failures. Sets `X-Aggregate-Partial: true` header.

Backends marked `required: true` still cause a 502 even in partial mode.

```json
{
  "user": {"id": "1", "name": "Alice"},
  "_errors": [{"backend": "orders", "error": "connection refused"}]
}
```

## Response Merging

- Backends with `group` set wrap their response under that JSON key
- Backends without `group` merge at the top level (JSON object keys merged)
- All backend calls execute in parallel
- Backends that return non-JSON are included as raw values under their group key

## Mutual Exclusions

Aggregate is mutually exclusive with: `echo`, `sequential`, `static`, `passthrough`.

## Admin API

```
GET /aggregate
```

Returns per-route aggregate handler stats including total requests, errors, and per-backend latencies.
