# Content Negotiation

Content negotiation parses the `Accept` header and re-encodes the response body in the requested format. Supports JSON, XML, and YAML output formats.

## Configuration

```yaml
routes:
  - id: api-flexible
    path: /api/data
    backends:
      - url: http://backend:8080
    content_negotiation:
      enabled: true
      supported:
        - json
        - xml
        - yaml
      default: json       # used when Accept is */*, empty, or omitted
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | false | Enable content negotiation |
| `supported` | list | required | Supported formats: `json`, `xml`, `yaml` |
| `default` | string | json | Default format for wildcard or missing Accept |

## Accept Header Parsing

The middleware parses the `Accept` header with quality factor support:

| Accept Value | Format |
|-------------|--------|
| `application/json`, `text/json` | json |
| `application/xml`, `text/xml` | xml |
| `application/yaml`, `text/yaml`, `application/x-yaml` | yaml |
| `*/*` | default format |

Quality factors (`q=`) are respected. Example: `Accept: application/xml;q=0.9, application/json;q=0.5` selects XML.

## Format Conversion

### JSON (passthrough)

When the best match is JSON, the response passes through unchanged with no buffering overhead.

### XML

JSON responses are converted to XML with the following rules:

- Root element: `<response>`
- Object keys become element names
- Arrays use `<item>` wrapper elements
- Values are XML-escaped
- Numeric keys get an underscore prefix
- `Content-Type: application/xml; charset=utf-8`

Example input:
```json
{"name": "Alice", "items": [1, 2, 3]}
```

Example output:
```xml
<?xml version="1.0" encoding="UTF-8"?>
<response><name>Alice</name><items><item>1</item><item>2</item><item>3</item></items></response>
```

### YAML

JSON responses are converted to YAML using `github.com/goccy/go-yaml` (already in the project dependencies).

- `Content-Type: application/yaml; charset=utf-8`

## Error Handling

- **406 Not Acceptable**: Returned when no supported format matches the Accept header
- **Conversion failure**: Falls back to original response if JSON-to-XML/YAML conversion fails

## Middleware Position

Step 17.4 in the middleware chain — after response body generator (17.35), before response validation (17.5).

## Mutual Exclusions

Content negotiation is mutually exclusive with `passthrough`.

## Admin API

```
GET /content-negotiation
```

Returns per-route stats including counts per format and 406 responses.
