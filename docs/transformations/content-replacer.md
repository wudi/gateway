---
title: "Content Replacer"
sidebar_position: 6
---

The content replacer applies regex-based string replacements to response bodies and/or headers. This is useful for rewriting internal URLs, redacting sensitive data, or modifying backend responses without changing the backend itself.

## How It Works

The middleware buffers the response body, applies regex replacements, then writes the modified body to the client. Header replacements are applied directly to response headers. Only text-like content types are processed (`text/*`, `application/json`, `application/xml`, `application/xhtml`); binary content is passed through unmodified.

## Configuration

Content replacer is per-route only.

```yaml
routes:
  - id: api
    path: /api
    content_replacer:
      enabled: true
      replacements:
        - pattern: "internal-api\\.example\\.com"
          replacement: "api.example.com"
          scope: body
        - pattern: "(\\w+)@internal\\.com"
          replacement: "${1}@external.com"
          scope: body
        - pattern: "internal-backend-\\d+"
          replacement: "backend"
          scope: "header:X-Server"
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable content replacement |
| `replacements` | list | - | List of replacement rules |

### Replacement Rule

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `pattern` | string | - | Go regex pattern |
| `replacement` | string | - | Replacement string (supports `$1`, `$2` capture groups) |
| `scope` | string | `body` | Where to apply: `body` or `header:<name>` |

## Scopes

- **`body`** (default): Replace matches in the response body. Only applied to text-like content types.
- **`header:<name>`**: Replace matches in the specified response header value.

## Capture Groups

Standard Go regex capture groups are supported:

```yaml
- pattern: "v(\\d+)\\.api"
  replacement: "v${1}.runway"
```

## Middleware Position

Step 17.3 in the per-route middleware chain -- after status code mapping (step 17.25) and before response validation (step 17.5). Skipped for passthrough routes.

## Admin API

```
GET /content-replacer
```

Returns per-route stats:
```json
{
  "api": {
    "total": 1000,
    "replaced": 342,
    "rules": 3
  }
}
```

## Validation

- At least one replacement rule is required when enabled
- All patterns must be valid Go regular expressions (validated at config load time)
- Content replacer is mutually exclusive with passthrough mode

## Example: URL Rewriting

Rewrite internal backend URLs to public-facing URLs:

```yaml
routes:
  - id: proxy
    path: /
    content_replacer:
      enabled: true
      replacements:
        - pattern: "https?://10\\.0\\.\\d+\\.\\d+(:\\d+)?"
          replacement: "https://api.example.com"
        - pattern: "X-Internal-ID"
          replacement: "X-Request-ID"
          scope: "header:X-Internal-ID"
```
