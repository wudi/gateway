---
title: "Debug Endpoint"
sidebar_position: 4
---

The debug endpoint provides request inspection, configuration summary, and runtime statistics through a configurable URL path. It is intercepted in `serveHTTP()` before route matching, so it works independently of route configuration.

## Configuration

```yaml
debug_endpoint:
  enabled: true
  path: /__debug    # default: /__debug
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable the debug endpoint |
| `path` | string | `/__debug` | URL path prefix for debug endpoints |

## Sub-Paths

### Request Echo

```
GET /__debug
GET /__debug/request
```

Echoes the full incoming request details:

```json
{
  "method": "POST",
  "url": "/__debug/request?foo=bar",
  "proto": "HTTP/1.1",
  "host": "localhost:8080",
  "remote_addr": "127.0.0.1:54321",
  "content_length": 13,
  "headers": {
    "Content-Type": ["application/json"],
    "X-Custom": ["value1", "value2"]
  },
  "query": {
    "foo": ["bar"]
  },
  "body": "{\"key\":\"val\"}",
  "timestamp": "2024-01-15T10:30:00.123456789Z"
}
```

Fields included:
- `method`, `url`, `proto`, `host`, `remote_addr`, `content_length`
- `headers` (multi-value)
- `query` (multi-value)
- `body` (up to 1MB, only if present)
- `tls` (version, server_name -- only for TLS connections)
- `timestamp` (RFC3339Nano)

### Config Summary

```
GET /__debug/config
```

Returns a sanitized view of the gateway configuration:

```json
{
  "routes": [
    {
      "id": "api",
      "path": "/api",
      "methods": ["GET", "POST"],
      "features": ["auth", "rate_limit", "cache"],
      "upstreams": 3
    }
  ],
  "listeners": [
    {
      "address": ":8080",
      "protocol": "http"
    }
  ],
  "registry": "consul"
}
```

### Runtime Stats

```
GET /__debug/runtime
```

Returns Go runtime information:

```json
{
  "goroutines": 42,
  "cpus": 8,
  "go_version": "go1.22.0",
  "uptime_seconds": 3600.5,
  "memory": {
    "alloc_bytes": 15728640,
    "total_alloc_bytes": 104857600,
    "sys_bytes": 31457280,
    "heap_alloc_bytes": 15728640,
    "heap_inuse_bytes": 16777216,
    "heap_objects": 125000
  },
  "gc": {
    "num_gc": 150,
    "pause_total_ns": 5000000,
    "last_pause_ns": 30000,
    "next_gc_bytes": 20971520
  }
}
```

## Security Considerations

The debug endpoint exposes internal configuration and runtime details. In production:

- Consider disabling it entirely (`enabled: false`)
- Use a non-obvious custom path if enabled
- Protect it with network-level access controls (IP filtering, VPN)
- The endpoint does not expose secrets, credentials, or environment variables

## Example

```yaml
debug_endpoint:
  enabled: true
  path: /_internal/debug
```

Then access:
- `GET /_internal/debug` -- echo your request
- `GET /_internal/debug/config` -- see config summary
- `GET /_internal/debug/runtime` -- see runtime stats
