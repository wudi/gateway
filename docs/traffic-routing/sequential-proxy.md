---
title: "Sequential Proxy (Backend Chaining)"
sidebar_position: 10
---

Sequential proxy chains multiple backend calls where each step's response is available to the next step's templates. This enables Backend-for-Frontend (BFF) patterns where data from one service enriches requests to the next.

## How It Works

Steps execute in order. Each step renders its URL, headers, and body from Go templates with accumulated context. After each step completes, the JSON response is parsed and stored as `Resp0`, `Resp1`, etc. The final step's response (status, headers, body) is returned to the client.

If any step fails, the chain aborts and returns `502 Bad Gateway`.

## Configuration

Sequential proxy is configured per route on `RouteConfig`. It replaces the normal proxy as the innermost handler (no `backends` required).

```yaml
routes:
  - id: user-enriched
    path: /users/:id
    sequential:
      enabled: true
      steps:
        - url: "http://user-service:8080/users/{{index .Request.PathParams \"id\"}}"
          method: GET
          timeout: 3s
        - url: "http://order-service:8080/orders?user_id={{index .Responses \"Resp0\" \"id\"}}"
          method: GET
          timeout: 5s
```

### Top-level fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable sequential proxy |
| `steps` | list | - | Ordered list of backend steps (min 2) |

### Step fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `url` | string | - | Go template for the backend URL (required) |
| `method` | string | `GET` | HTTP method |
| `headers` | map | - | Go template values for request headers |
| `body_template` | string | - | Go template for request body |
| `timeout` | duration | `5s` | Per-step timeout |

## Template Context

Each step's template receives a `StepContext`:

| Field | Type | Description |
|-------|------|-------------|
| `.Request.Method` | string | Original client request method |
| `.Request.URL` | string | Original request URL |
| `.Request.Host` | string | Request host |
| `.Request.Path` | string | URL path |
| `.Request.PathParams` | map[string]string | Route path parameters |
| `.Request.Query` | url.Values | Query parameters |
| `.Request.Headers` | http.Header | Request headers |
| `.Responses` | map[string]any | Step responses (keyed as `Resp0`, `Resp1`, etc.) |

Access previous step responses:
```
{{index .Responses "Resp0" "id"}}       # field "id" from step 0's JSON response
{{index .Responses "Resp1" "items"}}     # field "items" from step 1's JSON response
```

## Template Functions

| Function | Description | Example |
|----------|-------------|---------|
| `json` | JSON-encodes a value | `{{json (index .Responses "Resp0")}}` |

## Response Handling

- JSON responses are parsed into `map[string]any` and stored as `Resp0`, `Resp1`, etc.
- Non-JSON responses are stored as `{"_raw": "<body string>"}`.
- The final step's complete HTTP response (status code, headers, body) is forwarded to the client.

## Admin API

```
GET /sequential
```

Returns per-route stats:
```json
{
  "user-enriched": {
    "total_requests": 1000,
    "total_errors": 5,
    "steps": [
      {"errors": 2, "total_latency_us": 500000},
      {"errors": 3, "total_latency_us": 1200000}
    ]
  }
}
```

## Validation

- At least 2 steps required
- Each step must have a `url`
- Mutually exclusive with `echo`, `static`, and `passthrough`
- No `backends` field required (sequential makes its own HTTP calls)

## Example: BFF Pattern

Fetch a user, then use their `org_id` to fetch organization details, and finally assemble a combined payload:

```yaml
routes:
  - id: user-profile
    path: /profile/:user_id
    sequential:
      enabled: true
      steps:
        - url: "http://users:8080/users/{{index .Request.PathParams \"user_id\"}}"
          method: GET
          timeout: 3s
        - url: "http://orgs:8080/orgs/{{index .Responses \"Resp0\" \"org_id\"}}"
          method: GET
          timeout: 3s
        - url: "http://aggregator:8080/combine"
          method: POST
          timeout: 5s
          headers:
            Content-Type: application/json
          body_template: |
            {
              "user": {{json (index .Responses "Resp0")}},
              "org": {{json (index .Responses "Resp1")}}
            }
```
