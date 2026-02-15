# Backend Response Encoding

Auto-decode XML or YAML backend responses to JSON. This enables seamless integration with non-JSON backends — the backend returns XML/YAML, the gateway converts to JSON, and all downstream middleware (transforms, content negotiation, etc.) operate on JSON.

## Configuration

```yaml
routes:
  - id: legacy-xml-api
    path: /api/legacy
    backends:
      - url: http://xml-backend:8080
    backend_encoding:
      encoding: xml

  - id: config-api
    path: /api/config
    backends:
      - url: http://config-service:8080
    backend_encoding:
      encoding: yaml
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `encoding` | string | | Backend response format: `xml` or `yaml` |

## XML to JSON Conversion

XML elements are converted to JSON following these rules:

| XML Feature | JSON Result |
|------------|-------------|
| Elements | Object keys |
| Repeated elements | Arrays |
| Text content | String values (auto-detects numbers/booleans) |
| Attributes | Prefixed with `@` (e.g., `@id`) |
| Empty elements | Empty string |
| Mixed content (text + children) | `#text` key for text content |

### Example

```xml
<response>
  <user id="42">
    <name>alice</name>
    <active>true</active>
  </user>
  <item>one</item>
  <item>two</item>
</response>
```

Becomes:

```json
{
  "user": {
    "@id": 42,
    "name": "alice",
    "active": true
  },
  "item": ["one", "two"]
}
```

## YAML to JSON Conversion

YAML is parsed and re-serialized as JSON. All YAML types are preserved:

```yaml
name: alice
age: 30
tags:
  - admin
  - user
```

Becomes:

```json
{"name": "alice", "age": 30, "tags": ["admin", "user"]}
```

## Error Handling

If decoding fails (malformed XML/YAML), the original response is passed through unchanged. The error counter is incremented in stats.

Content-Type matching:
- XML: Content-Type must contain `xml` (e.g., `application/xml`, `text/xml`)
- YAML: Content-Type must contain `yaml` or `x-yaml`

If the backend's Content-Type doesn't match the configured encoding, the response passes through unchanged.

On successful conversion, the response Content-Type is set to `application/json`.

## Mutual Exclusions

- `backend_encoding` is mutually exclusive with `passthrough`

## Middleware Position

Step 17.55 — wraps the innermost handler (closest to the proxy), before response validation (17.5). This ensures encoding happens first on the response path so all downstream middleware operates on JSON.

## Admin API

`GET /backend-encoding` returns per-route encoding stats:

```json
{
  "legacy-xml-api": {
    "encoding": "xml",
    "encoded": 1500,
    "errors": 3
  }
}
```
