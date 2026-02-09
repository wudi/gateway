# Custom Error Pages

Render custom error responses with template-based HTML, JSON, and XML formats. Supports per-route configuration, status code mapping with fallback chains, and content negotiation via the `Accept` header.

## Configuration

### Global Defaults

```yaml
error_pages:
  enabled: true
  pages:
    "404":
      json: '{"error":"not found","code":{{.StatusCode}}}'
      html: '<h1>{{.StatusCode}} — Not Found</h1><p>The requested path {{.RequestPath}} was not found.</p>'
    "5xx":
      json: '{"error":"server error","code":{{.StatusCode}}}'
    "default":
      json: '{"error":"{{.StatusText}}","code":{{.StatusCode}}}'
      html: '<h1>Error {{.StatusCode}}</h1><p>{{.StatusText}}</p>'
```

### Per-Route Override

```yaml
routes:
  - id: my-api
    path: /api
    backends:
      - url: http://backend:8080
    error_pages:
      enabled: true
      pages:
        "404":
          json: '{"error":"resource not found","request_id":"{{.RequestID}}"}'
        "429":
          json: '{"error":"rate limited","retry_after":60}'
```

Per-route keys override global keys. Unmatched global keys are inherited.

### File-Based Templates

```yaml
error_pages:
  enabled: true
  pages:
    "default":
      html_file: /etc/gateway/error.html
      json_file: /etc/gateway/error.json
```

Inline and file are mutually exclusive per format (`html` vs `html_file`, etc.). File contents are read and compiled at startup.

## Status Code Matching

Page keys determine which status codes are matched:

| Key | Matches |
|-----|---------|
| `"404"` | Exactly status 404 |
| `"4xx"` | Any status 400-499 |
| `"5xx"` | Any status 500-599 |
| `"default"` | Any error status (>= 400) |

### Fallback Chain

When an error status code occurs, the lookup order is:

1. **Exact match** — e.g., `"404"` for status 404
2. **Class match** — e.g., `"4xx"` for any 4xx status
3. **Default** — `"default"` key
4. **Pass through** — if no match, the original response is sent unmodified

## Content Negotiation

The response format is chosen from the client's `Accept` header:

| Accept Header | Format |
|---------------|--------|
| `text/html` | HTML |
| `application/json` | JSON |
| `application/xml`, `text/xml` | XML |
| `*/*` or unrecognized | JSON (default) |

If the negotiated format has no template for the matched page, the best available format is used instead.

## Template Variables

All templates (HTML, JSON, XML) are Go `text/template` templates with access to these variables:

| Variable | Description |
|----------|-------------|
| `{{.StatusCode}}` | HTTP status code (e.g., 404) |
| `{{.StatusText}}` | HTTP status text (e.g., "Not Found") |
| `{{.ErrorMessage}}` | Error message if available |
| `{{.RequestID}}` | X-Request-ID value |
| `{{.RequestMethod}}` | HTTP method (GET, POST, etc.) |
| `{{.RequestPath}}` | Request URL path |
| `{{.Host}}` | Request Host header |
| `{{.Timestamp}}` | Current time in RFC3339 format |
| `{{.RouteID}}` | Gateway route ID |

### Example: JSON Error Template

```
{"error":"{{.StatusText}}","code":{{.StatusCode}},"path":"{{.RequestPath}}","request_id":"{{.RequestID}}","timestamp":"{{.Timestamp}}"}
```

### Example: HTML Error Template

```html
<!DOCTYPE html>
<html>
<head><title>Error {{.StatusCode}}</title></head>
<body>
  <h1>{{.StatusCode}} — {{.StatusText}}</h1>
  <p>Request ID: {{.RequestID}}</p>
</body>
</html>
```

## Middleware Placement

Error pages are applied at middleware step 4.1, after variable context setup (step 4) and before access logging (step 4.25). This means error pages intercept errors from:

- Rate limiting (429)
- Authentication (401, 403)
- WAF (403)
- Circuit breaker (503)
- Throttle/priority (503)
- Validation (400)
- Proxy errors (502, 504)

Errors from IP filtering (step 2) and CORS (step 3) are **not** intercepted, as they occur before the error pages middleware.

## Admin API

```bash
curl http://localhost:8081/error-pages
```

Returns per-route configuration and render metrics:

```json
{
  "my-api": {
    "pages": ["404", "5xx", "default"],
    "metrics": {
      "total_rendered": 42
    }
  }
}
```

## Validation Rules

- Page keys must be exact status codes (100-599), class patterns (`1xx`-`5xx`), or `"default"`
- Inline and file are mutually exclusive per format (e.g., `html` + `html_file` is an error)
- Each page entry must define at least one format (html, json, or xml)
- Inline templates must be valid Go `text/template` syntax
- File paths must exist at config load time
