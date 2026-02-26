---
title: "Data Manipulation"
sidebar_position: 2
---

The gateway provides several features for transforming, querying, and manipulating request and response data at the route level. These features operate on JSON bodies and HTTP headers/cookies/query parameters.

## JMESPath Query Language

Apply a [JMESPath](https://jmespath.org/) expression to JSON response bodies, extracting or reshaping data before returning it to the client.

### Configuration

```yaml
routes:
  - id: api
    path: /api/users
    backends:
      - url: http://backend:8080
    jmespath:
      enabled: true
      expression: "users[?active].{name: name, email: email}"
      wrap_collections: false
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable JMESPath filtering |
| `expression` | string | *required* | JMESPath expression to apply |
| `wrap_collections` | bool | `false` | Wrap array results in `{"collection": [...]}` |

### How It Works

The middleware buffers the JSON response body, applies the compiled JMESPath expression, and re-encodes the result as JSON. Non-JSON responses pass through unmodified. The expression is compiled once at config load time.

When `wrap_collections` is true, if the JMESPath expression returns an array, the result is wrapped in an object: `{"collection": [...]}`

### Examples

Extract specific fields from a list:

```yaml
jmespath:
  enabled: true
  expression: "items[].{id: id, name: name}"
```

Filter and sort:

```yaml
jmespath:
  enabled: true
  expression: "sort_by(items[?status == 'active'], &created_at)"
```

### Admin API

```
GET /jmespath
```

Returns per-route stats:
```json
{
  "api": {
    "applied": 1500,
    "wrap_collections": false
  }
}
```

---

## Field-Level Content Replacer

Apply targeted string transformations to specific fields within JSON response bodies using gjson paths.

### Configuration

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    field_replacer:
      enabled: true
      operations:
        - field: "user.email"
          type: regexp
          find: "(\\w{2})\\w+@"
          replace: "${1}***@"
        - field: "user.name"
          type: upper
        - field: "data.description"
          type: literal
          find: "internal"
          replace: "public"
        - field: "data.notes"
          type: trim
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable field replacement |
| `operations` | list | *required* | List of replacement operations |

### Operation Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `field` | string | *required* | gjson path to the target field |
| `type` | string | *required* | Operation type: `regexp`, `literal`, `upper`, `lower`, `trim` |
| `find` | string | - | Pattern to find (required for `regexp` and `literal`; optional chars for `trim`) |
| `replace` | string | - | Replacement string (for `regexp` and `literal`) |

### Operation Types

- **`regexp`** -- Apply a Go regex substitution. `find` is the regex pattern, `replace` is the substitution (supports `$1`, `$2` groups).
- **`literal`** -- Replace all occurrences of the literal `find` string with `replace`.
- **`upper`** -- Convert the field value to uppercase.
- **`lower`** -- Convert the field value to lowercase.
- **`trim`** -- Trim whitespace from the field value. If `find` is set, trim those specific characters instead.

Only string-typed JSON fields are processed. Non-string fields and missing paths are silently skipped.

### Admin API

```
GET /field-replacer
```

Returns per-route stats:
```json
{
  "api": {
    "operations": 4,
    "processed": 850
  }
}
```

---

## Martian-Style Modifiers

A composable pipeline of request/response modifiers inspired by Google's Martian proxy. Modifiers support conditional execution, else branches, priority-based ordering, and scoped application (request, response, or both).

### Configuration

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    modifiers:
      - type: header_copy
        from: X-Request-ID
        to: X-Correlation-ID
        scope: request
      - type: header_set
        name: X-Runway-Version
        value: "2.0"
        scope: response
      - type: cookie
        name: session_source
        value: runway
        domain: .example.com
        path: /
        max_age: 3600
        secure: true
        http_only: true
        same_site: lax
        scope: response
      - type: query
        params:
          api_version: "2"
          format: json
        scope: request
      - type: stash
        name: X-Original-URL
        scope: request
      - type: port
        port: 8443
        scope: request
```

### Modifier Types

| Type | Description | Required Fields |
|------|-------------|-----------------|
| `header_copy` | Copy a header value from one name to another | `from`, `to` |
| `header_set` | Set a header to a static value | `name`, `value` |
| `cookie` | Add or set a cookie | `name` |
| `query` | Add/override query parameters | `params` |
| `stash` | Save the original URL in a header before rewriting | `name` (default: `X-Original-URL`) |
| `port` | Override the port component of the request URL | `port` |

### Common Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `type` | string | *required* | Modifier type (see table above) |
| `scope` | string | `both` | When to apply: `request`, `response`, or `both` |
| `priority` | int | `0` | Execution priority (higher values run first) |
| `condition` | object | - | Optional condition for conditional execution |
| `else` | object | - | Modifier to apply when condition is false |

### Cookie Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `name` | string | *required* | Cookie name |
| `value` | string | - | Cookie value |
| `domain` | string | - | Cookie domain |
| `path` | string | - | Cookie path |
| `max_age` | int | - | Cookie max age in seconds |
| `secure` | bool | `false` | Secure flag |
| `http_only` | bool | `false` | HttpOnly flag |
| `same_site` | string | - | SameSite attribute: `lax`, `strict`, `none` |

### Conditional Execution

Modifiers can be executed conditionally based on request attributes:

```yaml
modifiers:
  - type: header_set
    name: X-Internal
    value: "true"
    scope: request
    condition:
      type: header
      name: X-Source
      value: "^internal-.*"
    else:
      type: header_set
      name: X-Internal
      value: "false"
```

#### Condition Types

| Type | Description |
|------|-------------|
| `header` | Check request header existence or value |
| `cookie` | Check request cookie existence or value |
| `query` | Check query parameter existence or value |
| `path_regex` | Match request path against regex |

The `name` field specifies which header/cookie/query parameter to check. The `value` field is an optional regex pattern -- if omitted, only existence is checked.

### Priority and FIFO Ordering

Modifiers are sorted by `priority` (higher values execute first). Modifiers with the same priority preserve their declaration order (stable sort).

### Admin API

```
GET /modifiers
```

Returns per-route stats:
```json
{
  "api": {
    "modifier_count": 6,
    "applied": 2500
  }
}
```

---

## Error Handling Modes

Control how backend error responses (status >= 400) are reformatted before returning to the client.

### Configuration

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    error_handling:
      mode: pass_status
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mode` | string | `default` | Error handling mode |

### Modes

**`default`** -- Pass through backend error responses unchanged.

**`pass_status`** -- Replace the body with a JSON envelope containing the status code:

```json
{"error": "gateway error", "status": 502}
```

The original HTTP status code is preserved.

**`detailed`** -- Include the route ID and original response body in a structured JSON envelope:

```json
{
  "error_my-route": {
    "status": 502,
    "body": "upstream connection refused"
  }
}
```

The original HTTP status code is preserved.

**`message`** -- Return a generic message with a 200 OK status, hiding the actual error from the client:

```json
{"message": "backend returned error", "status": 502}
```

The HTTP status code is overridden to 200.

### Admin API

```
GET /error-handling
```

Returns per-route stats:
```json
{
  "api": {
    "mode": "pass_status",
    "total": 5000,
    "reformatted": 42
  }
}
```

---

## Lua Scripting

Execute Lua scripts during the request and/or response phases. Scripts are pre-compiled at config load time and executed in pooled Lua VMs for performance. Only safe standard libraries are loaded (base, string, table, math), plus utility modules (json, base64, url, re, log).

### Configuration

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    lua:
      enabled: true
      request_script: |
        local auth = req:get_header("Authorization")
        if auth == "" then
          req:set_header("Authorization", "Bearer default-token")
        end

        -- Access runway context
        local rid = ctx:route_id()
        log.info("processing route: " .. rid)

        -- Early termination (return status, body)
        if req:get_header("X-Block") ~= "" then
          return 403, "blocked by lua"
        end
      response_script: |
        local body = resp:body()
        local status = resp:status()
        if status >= 400 then
          resp:set_header("X-Error", "true")
        end

        -- JSON manipulation
        local data = json.decode(body)
        if data then
          data.processed = true
          resp:set_body(json.encode(data))
        end
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable Lua scripting |
| `request_script` | string | - | Lua code for request phase |
| `response_script` | string | - | Lua code for response phase |

At least one of `request_script` or `response_script` must be provided when enabled.

### Request Phase API

The `req` global is available in request scripts:

| Method | Description |
|--------|-------------|
| `req:get_header(name)` | Get a request header value |
| `req:set_header(name, value)` | Set a request header |
| `req:del_header(name)` | Delete a request header |
| `req:path()` | Get the request path |
| `req:method()` | Get the HTTP method |
| `req:query_param(name)` | Get a query parameter value |
| `req:host()` | Get the request host |
| `req:scheme()` | Get `http` or `https` |
| `req:remote_addr()` | Get the remote address |
| `req:body()` | Read the request body (buffered for re-read) |
| `req:set_body(string)` | Replace the request body |
| `req:cookie(name)` | Get a cookie value |
| `req:set_path(path)` | Rewrite the request path |
| `req:set_query(query)` | Rewrite the query string |

Request scripts can return two values to short-circuit the request:

```lua
return 403, "forbidden"  -- returns 403 with body, skips backend
```

### Response Phase API

The `resp` global is available in response scripts:

| Method | Description |
|--------|-------------|
| `resp:get_header(name)` | Get a response header value |
| `resp:set_header(name, value)` | Set a response header |
| `resp:del_header(name)` | Delete a response header |
| `resp:status()` | Get the HTTP status code |
| `resp:set_status(code)` | Set the HTTP status code |
| `resp:body()` | Get the response body as a string |
| `resp:set_body(string)` | Replace the response body |

### Gateway Context (`ctx`)

The `ctx` global provides access to gateway context in both request and response scripts:

| Method | Description |
|--------|-------------|
| `ctx:route_id()` | Current route ID |
| `ctx:request_id()` | Request ID |
| `ctx:tenant_id()` | Tenant ID |
| `ctx:client_id()` | Authenticated client ID |
| `ctx:auth_type()` | Auth method (jwt, api_key) |
| `ctx:claim(name)` | Get a JWT claim value |
| `ctx:geo_country()` | ISO 3166-1 alpha-2 country code |
| `ctx:geo_city()` | City name |
| `ctx:path_param(name)` | Get a path parameter |
| `ctx:get_var(name)` | Get a custom variable |
| `ctx:set_var(name, value)` | Set a custom variable |

### Utility Modules

All Lua scripts (both route-level and rule-level) have access to these modules:

| Module | Functions | Description |
|--------|-----------|-------------|
| `json` | `json.encode(table)`, `json.decode(string)` | JSON encode/decode |
| `base64` | `base64.encode(string)`, `base64.decode(string)` | Base64 encode/decode |
| `url` | `url.encode(string)`, `url.decode(string)` | URL percent encode/decode |
| `re` | `re.match(pattern, string)`, `re.find(pattern, string)` | Go regex match/find |
| `log` | `log.info(msg)`, `log.warn(msg)`, `log.error(msg)` | Structured logging via zap |

### Examples

**Auth-based header injection:**

```lua
-- request_script
local cid = ctx:client_id()
if cid ~= "" then
  req:set_header("X-Client-ID", cid)
  local sub = ctx:claim("sub")
  req:set_header("X-User-ID", sub)
end
```

**Geo-based routing:**

```lua
-- request_script
local country = ctx:geo_country()
if country == "EU" or country == "DE" or country == "FR" then
  req:set_header("X-Region", "eu")
else
  req:set_header("X-Region", "global")
end
```

**JSON response transformation:**

```lua
-- response_script
local body = resp:body()
local data = json.decode(body)
if data then
  data.metadata = {
    processed_at = os.time and os.time() or 0,
    route = ctx:route_id()
  }
  resp:set_body(json.encode(data))
end
```

### Security

Only safe Lua libraries are loaded: `base`, `string`, `table`, `math`. File I/O, OS, and network libraries are not available. Scripts are compiled once at startup and validated -- syntax errors cause a config load failure.

### Admin API

```
GET /lua
```

Returns per-route stats:
```json
{
  "api": {
    "requests_run": 5000,
    "responses_run": 5000,
    "errors": 2
  }
}
```

---

## Backend Response: is_collection

Control how array responses from backends are returned to the client.

### Configuration

```yaml
routes:
  - id: api
    path: /api/items
    backends:
      - url: http://backend:8080
    backend_response:
      is_collection: true
      collection_key: items
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `is_collection` | bool | `false` | Wrap array responses in an object |
| `collection_key` | string | `collection` | Key name for the wrapping object |

When `is_collection` is true and the backend returns a JSON array, the response is wrapped:

```json
// Backend returns: [{"id": 1}, {"id": 2}]
// Client receives: {"items": [{"id": 1}, {"id": 2}]}
```

---

## Output Encoding

Override the response content type negotiation with a config-declared encoding.

### Configuration

```yaml
routes:
  - id: api
    path: /api
    backends:
      - url: http://backend:8080
    output_encoding: xml
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `output_encoding` | string | - | Force output format: `json`, `xml`, `yaml`, `json-collection`, `string` |

When set, this overrides any `Accept` header-based content negotiation. The backend response is transcoded to the specified format.

---

## Validation

- JMESPath `expression` must be a valid JMESPath expression (compiled at config load)
- Field replacer requires at least one operation when enabled; `regexp` type operations must have valid Go regex patterns
- Modifier `type` must be one of: `header_copy`, `header_set`, `cookie`, `query`, `stash`, `port`
- Modifier condition `type` must be one of: `header`, `cookie`, `query`, `path_regex`; condition `value` must be a valid regex when set
- Error handling `mode` must be one of: `default`, `pass_status`, `detailed`, `message`
- Lua scripts must be valid Lua syntax (compiled at config load)
