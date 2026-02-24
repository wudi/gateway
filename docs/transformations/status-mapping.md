---
title: "HTTP Status Code Mapping"
sidebar_position: 10
---

Remap backend response status codes to different client-facing codes on a per-route basis. Useful for normalizing error codes across heterogeneous backends or hiding internal error details.

## Configuration

```yaml
routes:
  - id: api-route
    path: /api/
    path_prefix: true
    backends:
      - url: http://backend:8080
    status_mapping:
      enabled: true
      mappings:
        404: 200     # convert backend 404 to 200
        500: 503     # convert internal errors to service unavailable
        401: 403     # convert unauthorized to forbidden
```

## How It Works

The status mapping middleware wraps the `http.ResponseWriter` to intercept `WriteHeader` calls. When a backend returns a status code that exists in the `mappings` map, the middleware substitutes the mapped value before sending the response to the client.

- The response body is passed through unchanged
- Only the first `WriteHeader` call is intercepted (subsequent calls are ignored per HTTP spec)
- If no `WriteHeader` is called before `Write`, an implicit 200 is assumed (standard Go behavior) and can be remapped
- The middleware supports `http.Flusher` for streaming responses

## Middleware Position

Step 17.25 — after response body transforms (17) and before content replacer (17.3), response body generator (17.35), and response validation (17.5). This ensures body transforms see the original status code, while content replacer and response body generator see the remapped code. Circuit breaker outcome recording (step 11 in the serveHTTP flow) happens before status mapping, so circuit breaker decisions are based on the original backend status code, not the remapped one.

## Admin API

**GET `/status-mapping`** — returns per-route mapping stats:

```json
{
  "api-route": {
    "total": 1000,
    "remapped": 42,
    "mappings": {"404": 200, "500": 503}
  }
}
```

## Validation

- All mapping keys (source codes) must be valid HTTP status codes (100-599)
- All mapping values (target codes) must be valid HTTP status codes (100-599)
