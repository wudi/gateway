---
title: "PII Redaction Middleware"
sidebar_position: 16
---

The PII redaction middleware detects and masks personally identifiable information (PII) in request and response bodies. It uses a combination of built-in patterns and custom regex rules to identify sensitive data and replace it with redacted placeholders, preventing accidental PII leakage through the gateway.

## How It Works

1. The middleware intercepts the response body (and optionally the request body) before forwarding
2. For each configured pattern, it scans the body text using regex matching
3. Matched content is replaced with a configurable mask string (default: `[REDACTED]`)
4. Headers can also be redacted using the same pattern set
5. Only text-like content types are processed; binary content passes through unmodified

## Configuration

PII redaction is per-route only.

```yaml
routes:
  - id: user-api
    path: /api/users
    path_prefix: true
    backends:
      - url: http://user-service:8080
    pii_redaction:
      enabled: true
      scope: response                    # response (default), request, or both
      mask: "[REDACTED]"                 # replacement string (default: "[REDACTED]")
      builtin_patterns:                  # built-in pattern names to activate
        - email
        - credit_card
        - ssn
        - phone
      custom_patterns:                   # additional regex patterns
        - name: national_id
          pattern: "\\b[A-Z]{2}\\d{6}[A-Z]\\b"
        - name: date_of_birth
          pattern: "\\b\\d{4}-\\d{2}-\\d{2}\\b"
          mask: "[DOB_REDACTED]"         # per-pattern mask override
      redact_headers:                    # response/request headers to redact
        - "X-User-Email"
        - "X-Customer-Phone"
```

### Full Example with Multiple Routes

```yaml
routes:
  - id: user-api
    path: /api/users
    path_prefix: true
    backends:
      - url: http://user-service:8080
    pii_redaction:
      enabled: true
      scope: both
      builtin_patterns:
        - email
        - credit_card
        - ssn
        - phone
      custom_patterns:
        - name: api_key
          pattern: "(?i)api[_-]?key[\"']?\\s*[:=]\\s*[\"']?[a-zA-Z0-9]{20,}"
          mask: "[API_KEY_REDACTED]"
      redact_headers:
        - "Authorization"

  - id: logs-api
    path: /api/logs
    path_prefix: true
    backends:
      - url: http://log-service:8080
    pii_redaction:
      enabled: true
      scope: response
      builtin_patterns:
        - email
        - phone
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable PII redaction |
| `scope` | string | `response` | Where to apply: `response`, `request`, or `both` |
| `mask` | string | `[REDACTED]` | Default replacement string for matched PII |
| `builtin_patterns` | list | - | Built-in pattern names to activate |
| `custom_patterns` | list | - | Custom regex patterns |
| `custom_patterns[].name` | string | - | Pattern identifier (for metrics) |
| `custom_patterns[].pattern` | string | - | Go regex pattern |
| `custom_patterns[].mask` | string | global `mask` | Per-pattern mask override |
| `redact_headers` | list | - | Header names to apply redaction to |

## Built-in Patterns

| Name | Matches | Example |
|------|---------|---------|
| `email` | Email addresses | `user@example.com` |
| `credit_card` | Major credit card number formats (Visa, Mastercard, Amex, Discover) | `4111-1111-1111-1111` |
| `ssn` | US Social Security Numbers | `123-45-6789` |
| `phone` | US/international phone numbers with common separators | `+1 (555) 123-4567` |

Built-in patterns are pre-compiled and optimized. They use word boundaries and format-aware matching to minimize false positives.

## Scopes

- **`response`** (default) — Redacts PII in the response body returned to the client. The backend receives the original request unchanged.
- **`request`** — Redacts PII in the request body before forwarding to the backend. The client receives the original response unchanged.
- **`both`** — Redacts PII in both the request body (before forwarding) and the response body (before returning to client).

## Header Redaction

Headers listed in `redact_headers` are processed through the same pattern matching as body content. This applies to response headers when scope is `response`, request headers when scope is `request`, or both when scope is `both`.

```yaml
pii_redaction:
  enabled: true
  builtin_patterns:
    - email
  redact_headers:
    - "X-User-Email"      # header value is scanned for email patterns
    - "X-Debug-Info"       # any email addresses in this header are masked
```

## Content-Type Filtering

Only text-like content types are processed:

- `text/*`
- `application/json`
- `application/xml`
- `application/xhtml+xml`

Binary content types (images, protobuf, octet-stream, etc.) pass through without scanning. This prevents corruption of binary data and avoids unnecessary processing overhead.

## Middleware Position

Step **17.31** in the per-route middleware chain -- after content replacer (step 17.3) and before response validation (step 17.5). This position ensures that any content replacements happen first, and PII redaction operates on the final response body before validation.

## Mutual Exclusivity

PII redaction is mutually exclusive with passthrough mode. A route cannot have both `passthrough: true` and `pii_redaction.enabled: true` -- this is a config validation error. Passthrough mode skips body processing, which would defeat the purpose of PII scanning.

## Validation Rules

- At least one of `builtin_patterns` or `custom_patterns` must be provided when `enabled: true`
- `scope` must be `response`, `request`, or `both`
- All `custom_patterns[].pattern` entries must be valid Go regular expressions
- `custom_patterns[].name` must be non-empty
- PII redaction is mutually exclusive with `passthrough: true`

## Admin API

### GET `/pii-redaction`

Returns per-route PII redaction status and detection metrics.

```bash
curl http://localhost:8081/pii-redaction
```

**Response:**
```json
{
  "user-api": {
    "scope": "both",
    "builtin_patterns": ["email", "credit_card", "ssn", "phone"],
    "custom_patterns": ["api_key"],
    "total_processed": 8500,
    "total_redacted": 620,
    "redactions_by_pattern": {
      "email": 312,
      "credit_card": 45,
      "ssn": 18,
      "phone": 203,
      "api_key": 42
    },
    "headers_redacted": 150,
    "skipped_binary": 230
  },
  "logs-api": {
    "scope": "response",
    "builtin_patterns": ["email", "phone"],
    "custom_patterns": [],
    "total_processed": 3200,
    "total_redacted": 88,
    "redactions_by_pattern": {
      "email": 52,
      "phone": 36
    },
    "headers_redacted": 0,
    "skipped_binary": 10
  }
}
```

## Notes

- The middleware buffers the full response (or request) body in memory for scanning. For large bodies, combine with `body_limit` to cap the maximum size.
- Redaction is applied using `regexp.ReplaceAllString`. Capture groups in custom patterns are supported in the `mask` string (e.g., `mask: "${1}****"`), though this is rarely appropriate for PII.
- Built-in patterns are designed for common US/international formats. For locale-specific PII (national IDs, tax numbers), use `custom_patterns`.
- PII redaction operates on the serialized body text. It does not parse JSON structure -- patterns are matched against the raw body string. For field-level encryption of specific JSON paths, see [Field Encryption](field-encryption.md).

See [Content Replacer](../transformations/content-replacer.md) for general regex-based response rewriting.
See [Configuration Reference](../reference/configuration-reference.md) for all fields.
