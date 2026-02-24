---
title: "Dynamic IP Blocklist"
sidebar_position: 12
---

The dynamic IP blocklist subscribes to external threat intelligence feeds and blocks or logs requests from listed IP addresses. It supports both static IP/CIDR entries and auto-refreshing feeds in text or JSON format.

## Configuration

IP blocklist can be configured globally and per-route.

### Global Configuration

```yaml
ip_blocklist:
  enabled: true
  static:                        # always-blocked IPs/CIDRs
    - "203.0.113.0/24"
    - "198.51.100.50"
  action: block                  # "block" (default) or "log"
  feeds:
    - url: "https://feeds.example.com/bad-ips.txt"
      refresh_interval: 5m       # default 5m
      format: text               # "text" (default) or "json"
    - url: "https://feeds.example.com/threat-ips.json"
      refresh_interval: 10m
      format: json
```

### Per-Route Override

```yaml
routes:
  - id: api
    path: /api
    ip_blocklist:
      enabled: true
      static:
        - "192.0.2.0/24"
      action: block
      feeds:
        - url: "https://feeds.example.com/api-blocklist.txt"
          refresh_interval: 5m
          format: text
```

## How It Works

1. On startup, static entries are parsed into IP networks
2. Background goroutines fetch each feed at the configured `refresh_interval`
3. On each request, the client IP (from `X-Forwarded-For` / trusted proxy extraction) is checked against all static entries and feed entries
4. If matched and `action: block`, the request is rejected with 403 Forbidden
5. If matched and `action: log`, the request proceeds but a warning is logged

## Feed Formats

### Text Format

One IP or CIDR per line. Lines starting with `#` are treated as comments and skipped. Empty lines are ignored.

```
# Threat feed - updated daily
203.0.113.5
198.51.100.0/24
192.0.2.10
```

### JSON Format

A JSON array of IP/CIDR strings:

```json
["203.0.113.5", "198.51.100.0/24", "192.0.2.10"]
```

## Config Merging

When both global and per-route configs are enabled, they are merged:
- Static entries from both configs are combined (union)
- Feeds from both configs are combined
- Per-route `action` overrides the global action

## Middleware Position

IP blocklist runs at step **2.85** in the middleware chain — after bot detection (2.8) and before CORS (3). The global blocklist wraps the per-route blocklist, so both are checked.

## Admin API

### GET `/ip-blocklist`

Returns blocklist status for all routes.

```bash
curl http://localhost:8081/ip-blocklist
```

**Response (200 OK):**

```json
{
  "api": {
    "enabled": true,
    "action": "block",
    "static_entries": 3,
    "feed_count": 2,
    "total_blocked_ips": 1250,
    "blocked_requests": 42
  }
}
```

### POST `/ip-blocklist/refresh`

Forces an immediate refresh of all feeds (global and per-route).

```bash
curl -X POST http://localhost:8081/ip-blocklist/refresh
```

**Response (200 OK):**

```json
{
  "status": "ok",
  "refreshed": 3
}
```

## Validation

- `action` must be `"block"` or `"log"`
- `static` entries must be valid IPs or CIDRs
- Feed `url` is required for each feed entry
- Feed `format` must be `"text"` or `"json"`
- Feed `refresh_interval` must be >= 1s when specified
