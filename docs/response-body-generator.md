# Response Body Generator

The response body generator rewrites the entire response body using a Go `text/template`. This enables response reshaping, wrapping, and transformation without modifying backend services.

## Configuration

```yaml
routes:
  - id: api-wrapper
    path: /api/users
    backends:
      - url: http://backend:8080
    response_body_generator:
      enabled: true
      content_type: application/json    # default "application/json"
      template: |
        {
          "data": {{json .Parsed}},
          "meta": {
            "status": {{.StatusCode}},
            "path": "{{.Path}}",
            "route": "{{.RouteID}}"
          }
        }
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable response body generation |
| `template` | string | required | Go `text/template` string |
| `content_type` | string | application/json | Content-Type for the generated response |

## Template Data

The template receives a `TemplateData` struct with these fields:

| Field | Type | Description |
|-------|------|-------------|
| `.Body` | string | Raw response body as string |
| `.StatusCode` | int | Response status code |
| `.Headers` | http.Header | Response headers |
| `.Parsed` | interface{} | JSON-parsed body (nil if not valid JSON) |
| `.RouteID` | string | Route ID |
| `.Method` | string | Request method |
| `.Path` | string | Request path |
| `.PathParams` | map[string]string | Path parameters |
| `.Query` | url.Values | Query parameters |
| `.ClientIP` | string | Client IP address |

## Template Functions

- `json` — Marshal a value to JSON string
- `first` — Return first element of a string slice

## Error Handling

If the template execution fails, the original response is passed through unchanged.

## Middleware Position

Step 17.35 in the middleware chain — after content replacer (17.3), before content negotiation (17.4) and response validation (17.5).

## Mutual Exclusions

Response body generator is mutually exclusive with `passthrough`.

## Admin API

```
GET /response-body-generator
```

Returns per-route stats including generation count and content type.

**Response:**
```json
{
  "api-wrapper": {
    "generated": 4200,
    "content_type": "application/json"
  }
}
```

## Use Cases

### Response Wrapping

Wrap a backend's raw response in a standard envelope:

```yaml
response_body_generator:
  enabled: true
  template: |
    {
      "data": {{json .Parsed}},
      "meta": {
        "status": {{.StatusCode}},
        "path": "{{.Path}}"
      }
    }
```

Backend returns `{"name": "Alice"}`, client receives:
```json
{"data": {"name": "Alice"}, "meta": {"status": 200, "path": "/api/users/1"}}
```

### Field Extraction

Extract a single field from the backend response:

```yaml
response_body_generator:
  enabled: true
  template: '{{json .Parsed.results}}'
```

Backend returns `{"results": [1, 2, 3], "total": 3}`, client receives `[1,2,3]`.

### Dynamic Content-Type

Generate different output based on request context:

```yaml
response_body_generator:
  enabled: true
  content_type: text/plain
  template: '{{.Method}} {{.Path}} -> {{.StatusCode}}'
```

Returns: `GET /api/users -> 200`
