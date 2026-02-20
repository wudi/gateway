# Parameter Forwarding Control

Parameter forwarding provides zero-trust control over which request headers, query parameters, and cookies are forwarded to backend services. Only explicitly whitelisted parameters pass through.

## Configuration

```yaml
routes:
  - id: secure-api
    path: /api/data
    backends:
      - url: http://backend:8080
    param_forwarding:
      enabled: true
      headers:
        - Authorization
        - X-Request-ID
        - Accept
      query_params:
        - page
        - limit
        - sort
      cookies:
        - session
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable parameter forwarding control |
| `headers` | list | - | Allowed request headers (case-insensitive) |
| `query_params` | list | - | Allowed query parameter names |
| `cookies` | list | - | Allowed cookie names |

At least one of `headers`, `query_params`, or `cookies` must be non-empty when enabled.

## Essential Headers

The following headers are **always preserved** regardless of the whitelist:

- `Host`
- `Content-Type`
- `Content-Length`
- `Transfer-Encoding`
- `Connection`
- `Accept-Encoding`
- `User-Agent`

## Behavior

- **Headers**: All headers not in the whitelist (and not essential) are removed
- **Query params**: Query string is rebuilt with only allowed parameters
- **Cookies**: Cookie header is rebuilt with only allowed cookies
- Each category is independent — if `headers` is set but `cookies` is not, cookies pass through unfiltered

## Middleware Position

Step 16.1 in the middleware chain — after request body generator (16.05), before backend auth (16.25). This ensures parameter filtering happens just before the request is sent to the backend.

## Admin API

```
GET /param-forwarding
```

Returns per-route stats including stripped parameter count and whitelist sizes.

**Response:**
```json
{
  "secure-api": {
    "stripped": 142,
    "allowed_headers": 3,
    "allowed_query": 3,
    "allowed_cookies": 1
  }
}
```
