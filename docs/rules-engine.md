# Rules Engine

The rules engine evaluates expressions against request and response attributes to make routing decisions, modify headers, or block traffic. Rules use [expr-lang](https://github.com/expr-lang/expr) with a Cloudflare-style dot notation environment.

## Overview

Rules can be applied globally and per route, in two phases:
- **Request rules** — evaluated after authentication, before the backend proxy
- **Response rules** — evaluated after the backend responds, before sending to the client

## Expression Syntax

Expressions use dot notation to access request/response fields:

```
http.request.method == "POST" && ip.src == "10.0.0.1"
http.request.uri.path matches "^/api/v[0-9]+"
http.request.headers["Authorization"] != ""
auth.type == "jwt" && auth.claims["role"] == "admin"
http.response.code >= 500
```

### Available Fields

**Request fields:**

| Expression | Type | Description |
|-----------|------|-------------|
| `http.request.method` | string | HTTP method |
| `http.request.uri.path` | string | URL path |
| `http.request.uri.query` | string | Raw query string |
| `http.request.uri.full` | string | Full URI |
| `http.request.uri.args` | map | Query parameters |
| `http.request.headers` | map | Request headers (first value) |
| `http.request.cookies` | map | Request cookies |
| `http.request.host` | string | Host header |
| `http.request.scheme` | string | `http` or `https` |
| `http.request.body_size` | int64 | Content-Length |
| `ip.src` | string | Client IP address |
| `route.id` | string | Matched route ID |
| `route.params` | map | Path parameters |
| `auth.client_id` | string | Authenticated client ID |
| `auth.type` | string | Auth method (jwt, api_key) |
| `auth.claims` | map | JWT claims |

**Response fields** (response phase only):

| Expression | Type | Description |
|-----------|------|-------------|
| `http.response.code` | int | Response status code |
| `http.response.headers` | map | Response headers |
| `http.response.response_time` | float | Response time (ms) |

### Operators

Standard comparison (`==`, `!=`, `>`, `>=`, `<`, `<=`), logical (`&&`, `||`, `!`), string (`matches` for regex, `contains`, `startsWith`, `endsWith`), and membership (`in`).

## Rule Configuration

```yaml
rules:
  request:
    - id: "block-bad-ips"
      expression: 'ip.src == "1.2.3.4"'
      action: "block"
      status_code: 403

    - id: "redirect-old-api"
      expression: 'http.request.uri.path startsWith "/api/v1"'
      action: "redirect"
      redirect_url: "/api/v2"

    - id: "custom-error"
      expression: 'http.request.headers["X-Bad"] != ""'
      action: "custom_response"
      status_code: 400
      body: '{"error": "bad request header"}'

    - id: "add-headers"
      expression: 'auth.type == "jwt"'
      action: "set_headers"
      headers:
        add:
          X-Auth-Type: "jwt"

    - id: "rewrite-path"
      expression: 'http.request.uri.path matches "^/old/(.*)"'
      action: "rewrite"
      rewrite:
        path: "/new/$1"

    - id: "log-slow"
      expression: 'http.request.body_size > 1048576'
      action: "log"
      log_message: "Large request body detected"

  response:
    - id: "add-security-headers"
      expression: "true"
      action: "set_headers"
      headers:
        set:
          X-Content-Type-Options: "nosniff"
          X-Frame-Options: "DENY"
```

## Actions

### Request-Phase Actions

| Action | Description |
|--------|-------------|
| `block` | Return `status_code` (default 403) immediately |
| `custom_response` | Return `status_code` with custom `body` |
| `redirect` | Redirect to `redirect_url` |
| `set_headers` | Modify request headers |
| `rewrite` | Rewrite path, query, or headers |
| `group` | Assign request to a traffic split group |
| `log` | Log a message and continue processing |

### Response-Phase Actions

| Action | Description |
|--------|-------------|
| `set_headers` | Modify response headers |

Response rules cannot use terminating actions (`block`, `custom_response`, `redirect`) or request-only actions (`rewrite`, `group`).

## Per-Route Rules

Rules can be scoped to specific routes:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    rules:
      request:
        - id: "require-json"
          expression: 'http.request.method == "POST" && http.request.headers["Content-Type"] != "application/json"'
          action: "custom_response"
          status_code: 415
          body: '{"error": "Content-Type must be application/json"}'
```

Global rules execute before per-route rules in each phase.

## Rule Evaluation

Rules are evaluated in order within each phase. For request rules, the first matching terminating action (block, custom_response, redirect) stops evaluation and returns immediately. Non-terminating actions (set_headers, rewrite, log) execute and continue to the next rule.

Rules can be disabled without removal:

```yaml
- id: "temp-disabled"
  enabled: false
  expression: '...'
  action: "block"
```

## Key Config Fields

| Field | Type | Description |
|-------|------|-------------|
| `rules.request` | []RuleConfig | Request-phase rules |
| `rules.response` | []RuleConfig | Response-phase rules |
| `id` | string | Unique rule identifier |
| `expression` | string | expr-lang expression |
| `action` | string | Action to take on match |
| `status_code` | int | HTTP status (for block/custom_response) |
| `redirect_url` | string | Redirect target URL |
| `headers` | HeaderTransform | Header modifications |
| `rewrite.path` | string | New path for rewrite action |

See [Configuration Reference](configuration-reference.md#rules-global) for all fields.
