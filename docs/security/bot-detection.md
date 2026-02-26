---
title: "Bot Detection"
sidebar_position: 9
---

The runway provides regex-based User-Agent blocking to reject requests from known bots, scrapers, and crawlers.

## Configuration

Bot detection can be configured globally and per route. Per-route config merges with (overrides) global settings.

```yaml
# Global bot detection
bot_detection:
  enabled: true
  deny:
    - "(?i)googlebot"
    - "(?i)bingbot"
    - "(?i)scrapy"
  allow:
    - "(?i)googlebot-news"    # override: allow Googlebot News

# Per-route override
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    bot_detection:
      enabled: true
      deny:
        - "(?i)curl/"
        - "(?i)wget/"
      allow:
        - "(?i)curl/.*healthcheck"
```

## How It Works

1. The `User-Agent` header is checked against each `deny` pattern
2. If a deny pattern matches, the `allow` patterns are checked for an override
3. If denied and not allowed, the request is rejected with `403 Forbidden`
4. If no deny pattern matches, the request proceeds normally
5. Requests with no `User-Agent` header are allowed through (not denied)

All regex patterns are compiled at initialization time, not per-request.

## Config Merging

When both global and per-route bot detection are enabled, the per-route config takes precedence. If only the global config is enabled, it applies to all routes.

## Middleware Position

Bot detection runs at step 2.8 in the middleware chain — after maintenance mode checks and before CORS. This blocks bots early before any expensive processing (auth, rate limiting, proxying).

## Admin API

**`GET /bot-detection`** returns per-route stats:

```json
{
  "api": {
    "enabled": true,
    "deny_patterns": 2,
    "allow_patterns": 1,
    "blocked": 42
  }
}
```

## Validation

- At least one `deny` pattern is required when enabled
- All regex patterns must be valid Go regular expressions
- Invalid patterns cause a config validation error at startup
