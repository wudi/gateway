# Body Generator

The body generator creates a request body from a Go `text/template`, populated with request context (path params, query params, headers, original body, client IP). This is useful for GET-to-POST translation, constructing backend API payloads from URL parameters, or normalizing request formats.

## How It Works

A Go template is compiled at startup. On each request, the template is executed with request context data and the result replaces `r.Body`. The `Content-Type` and `Content-Length` headers are updated accordingly.

## Configuration

Body generator is configured per route on `RouteConfig`.

```yaml
routes:
  - id: search
    path: /search
    backends:
      - url: http://search-service:8080
    body_generator:
      enabled: true
      template: '{"query": "{{.Query.Get "q"}}", "client": "{{.ClientIP}}"}'
      content_type: application/json
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable body generation |
| `template` | string | - | Go `text/template` string (required) |
| `content_type` | string | `application/json` | Content-Type header for the generated body |

## Template Data

The following fields are available in the template:

| Field | Type | Description |
|-------|------|-------------|
| `.Method` | string | HTTP method (GET, POST, etc.) |
| `.URL` | string | Full request URL |
| `.Host` | string | Request host |
| `.Path` | string | URL path |
| `.PathParams` | map[string]string | Route path parameters |
| `.Query` | url.Values | Query string parameters (use `.Query.Get "key"`) |
| `.Headers` | http.Header | Request headers (use `.Headers.Get "key"`) |
| `.Body` | string | Original request body (if present) |
| `.ClientIP` | string | Client IP address |
| `.RouteID` | string | Route identifier |

## Template Functions

| Function | Description | Example |
|----------|-------------|---------|
| `json` | JSON-encodes a value | `{{json .PathParams}}` |
| `first` | Returns first element of a string slice | `{{first (.Query "tags")}}` |

## Middleware Position

Step 16.05 in the per-route middleware chain -- after request transforms (step 16) and before backend auth (step 16.25).

## Admin API

```
GET /body-generator
```

Returns per-route stats:
```json
{
  "search": {
    "generated": 5000,
    "content_type": "application/json"
  }
}
```

## Example: GET-to-POST Translation

Convert a GET search request into a POST JSON body for the backend:

```yaml
routes:
  - id: search-proxy
    path: /api/search
    backends:
      - url: http://elasticsearch:9200/index/_search
    body_generator:
      enabled: true
      content_type: application/json
      template: |
        {
          "query": {
            "match": {
              "content": "{{.Query.Get "q"}}"
            }
          },
          "size": 10
        }
```

## Example: Path-to-Body Mapping

Use path parameters to construct a backend request:

```yaml
routes:
  - id: user-lookup
    path: /users/:id/profile
    backends:
      - url: http://user-service:8080/graphql
    body_generator:
      enabled: true
      template: '{"query": "{ user(id: \"{{index .PathParams "id"}}\") { name email } }"}'
```
