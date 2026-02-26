---
title: "Rules Engine"
sidebar_position: 3
---

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
geo.country == "US" || geo.country == "CA"
geo.country in ["US", "GB", "DE"] && auth.type == "jwt"
geo.city != "Beijing"
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
| `geo.country` | string | ISO 3166-1 alpha-2 country code (requires geo enabled) |
| `geo.country_name` | string | Country name in English |
| `geo.city` | string | City name |
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

    - id: "block-restricted-countries"
      expression: 'geo.country in ["CN", "RU", "IR"]'
      action: "block"
      status_code: 451

    - id: "rate-limit-header-by-country"
      expression: 'geo.country != "US"'
      action: "set_headers"
      headers:
        set:
          X-Rate-Tier: "international"

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
| `delay` | Pause request processing for `delay` duration |
| `set_var` | Set custom variables on the request context |
| `cache_bypass` | Mark the request to skip cache lookup |
| `lua` | Execute inline Lua script with full gateway context |

### Response-Phase Actions

| Action | Description |
|--------|-------------|
| `set_headers` | Modify response headers |
| `log` | Log a message and continue processing |
| `set_status` | Override the response status code |
| `set_body` | Replace the response body |
| `lua` | Execute inline Lua script with response access |

Response rules cannot use terminating actions (`block`, `custom_response`, `redirect`) or request-only actions (`rewrite`, `group`, `delay`, `set_var`, `cache_bypass`). Response-only actions (`set_status`, `set_body`) cannot be used in request rules.

### New Action Examples

**Delay** — add latency for testing or rate shaping:

```yaml
rules:
  request:
    - id: "slow-down-scraper"
      expression: 'http.request.headers["User-Agent"] contains "Scrapy"'
      action: "delay"
      delay: 2s
```

**Set Variable** — set custom variables for downstream middleware:

```yaml
rules:
  request:
    - id: "mark-premium"
      expression: 'auth.claims["tier"] == "premium"'
      action: "set_var"
      variables:
        user_tier: "premium"
        rate_limit_override: "1000"
```

**Cache Bypass** — skip cache for specific requests:

```yaml
rules:
  request:
    - id: "bypass-cache-for-admin"
      expression: 'auth.claims["role"] == "admin"'
      action: "cache_bypass"
```

**Set Status** — override response status code:

```yaml
rules:
  response:
    - id: "mask-404-as-200"
      expression: 'http.response.code == 404 && http.request.uri.path startsWith "/api/optional"'
      action: "set_status"
      status_code: 200
```

**Set Body** — replace response body:

```yaml
rules:
  response:
    - id: "custom-error-body"
      expression: 'http.response.code >= 500'
      action: "set_body"
      body: '{"error": "service unavailable"}'
```

**Lua** — run inline Lua with full gateway context:

```yaml
rules:
  request:
    - id: "lua-auth-routing"
      expression: 'auth.type == "jwt"'
      action: "lua"
      lua_script: |
        local claims_sub = ctx:claim("sub")
        req:set_header("X-User-ID", claims_sub)
        local data = json.encode({user = claims_sub})
        req:set_header("X-User-Data", data)

  response:
    - id: "lua-response-transform"
      expression: 'http.response.code == 200'
      action: "lua"
      lua_script: |
        local body = resp:body()
        local data = json.decode(body)
        data.runway_processed = true
        resp:set_body(json.encode(data))
```

The `lua` action provides access to `req`/`resp` objects, a `ctx` object for gateway context (route ID, auth claims, geo data, custom variables), and utility modules (`json`, `base64`, `url`, `re`, `log`). See the [Lua Scripting](#lua-scripting-in-rules) section below for full API details.

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
| `status_code` | int | HTTP status (for block/custom_response/set_status) |
| `body` | string | Response body (for custom_response/set_body) |
| `redirect_url` | string | Redirect target URL |
| `headers` | HeaderTransform | Header modifications |
| `rewrite.path` | string | New path for rewrite action |
| `delay` | duration | Delay duration (e.g. `500ms`, `2s`) |
| `variables` | map | Key-value pairs for set_var action |
| `lua_script` | string | Inline Lua code for lua action |

See [Configuration Reference](configuration-reference.md#rules-global) for all fields.

## Lua Scripting in Rules {#lua-scripting-in-rules}

The `lua` rule action executes inline Lua scripts with access to the full gateway context. Scripts are pre-compiled at config load time and run in pooled VMs.

### Available Globals

**Request phase:** `req`, `ctx`, plus utility modules
**Response phase:** `resp`, `ctx`, plus utility modules

### Request Object (`req`)

| Method | Description |
|--------|-------------|
| `req:get_header(name)` | Get a request header |
| `req:set_header(name, value)` | Set a request header |
| `req:del_header(name)` | Delete a request header |
| `req:path()` | Get request path |
| `req:method()` | Get HTTP method |
| `req:query_param(name)` | Get a query parameter |
| `req:host()` | Get request host |
| `req:scheme()` | Get `http` or `https` |
| `req:remote_addr()` | Get remote address |
| `req:body()` | Read request body (buffered for re-read) |
| `req:set_body(string)` | Replace request body |
| `req:cookie(name)` | Get a cookie value |
| `req:set_path(path)` | Rewrite request path |
| `req:set_query(query)` | Rewrite query string |

### Response Object (`resp`)

| Method | Description |
|--------|-------------|
| `resp:get_header(name)` | Get a response header |
| `resp:set_header(name, value)` | Set a response header |
| `resp:del_header(name)` | Delete a response header |
| `resp:status()` | Get response status code |
| `resp:set_status(code)` | Set response status code |
| `resp:body()` | Get response body |
| `resp:set_body(string)` | Replace response body |

### Context Object (`ctx`)

| Method | Description |
|--------|-------------|
| `ctx:route_id()` | Current route ID |
| `ctx:request_id()` | Request ID |
| `ctx:tenant_id()` | Tenant ID |
| `ctx:client_id()` | Authenticated client ID |
| `ctx:auth_type()` | Auth method (jwt, api_key) |
| `ctx:claim(name)` | Get a JWT claim value |
| `ctx:geo_country()` | ISO country code |
| `ctx:geo_city()` | City name |
| `ctx:path_param(name)` | Get a path parameter |
| `ctx:get_var(name)` | Get a custom variable |
| `ctx:set_var(name, value)` | Set a custom variable |

### Utility Modules

| Module | Functions | Description |
|--------|-----------|-------------|
| `json` | `json.encode(table)`, `json.decode(string)` | JSON encode/decode |
| `base64` | `base64.encode(string)`, `base64.decode(string)` | Base64 encode/decode |
| `url` | `url.encode(string)`, `url.decode(string)` | URL encode/decode |
| `re` | `re.match(pattern, string)`, `re.find(pattern, string)` | Go regex match/find |
| `log` | `log.info(msg)`, `log.warn(msg)`, `log.error(msg)` | Structured logging |
