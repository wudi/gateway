# Mock Responses

The gateway can return static mock responses without calling the backend. This is useful for development, testing, API prototyping, and graceful degradation.

## Configuration

Mock responses are configured per route:

```yaml
routes:
  - id: "mock-api"
    path: "/api/v2/status"
    backends:
      - url: "http://backend:9000"
    mock_response:
      enabled: true
      status_code: 200
      headers:
        Content-Type: "application/json"
        X-Mock: "true"
      body: '{"status":"ok","version":"2.0"}'
```

## How It Works

When enabled, the mock response middleware intercepts the request at step 7.75 in the middleware chain (after WAF and fault injection, before body limits). The configured status code, headers, and body are returned immediately — the request never reaches the backend.

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable mock responses |
| `status_code` | int | `200` | HTTP status code to return |
| `headers` | map | `{}` | Response headers to set |
| `body` | string | `""` | Response body string |

## Validation

- `status_code` must be between 100 and 599 (if set)
- Cannot be combined with `echo: true` on the same route (mutually exclusive)

## Admin API

**`GET /mock-responses`** returns per-route stats:

```json
{
  "mock-api": {
    "enabled": true,
    "status_code": 200,
    "served": 156
  }
}
```

## Use Cases

- **API prototyping**: Define response shapes before backends are ready
- **Testing**: Return known responses for integration test environments
- **Graceful degradation**: Serve cached/static fallback when backends are down
- **Development**: Mock third-party APIs locally
