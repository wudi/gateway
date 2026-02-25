# AI Crawl Control

AI crawl control detects known AI crawlers (GPTBot, ClaudeBot, CCBot, etc.) and enforces per-crawler policies. It includes a built-in database of 17+ known AI crawlers, per-crawler allow/block/monitor actions, path-level enforcement, and a monitoring dashboard via admin API.

## How It Works

1. **Pre-filter** — A single combined regex checks the User-Agent against all known crawler patterns. Non-matching UAs (99%+ of traffic) are rejected in one call with zero per-crawler iteration.
2. **Identification** — For UAs that match the pre-filter, individual crawler patterns are checked (custom crawlers first, then built-in) to identify the specific crawler.
3. **Path enforcement** — If the matching policy has `allow_paths` or `disallow_paths`, the request path is checked using `doublestar.PathMatch` (`*` matches within a segment, `**` matches across segments).
4. **Action** — The resolved action (`block`, `monitor`, or `allow`) is applied and metrics are recorded.

## Configuration

### Global (applies to all routes)

```yaml
ai_crawl_control:
  enabled: true
  default_action: monitor   # "monitor" (default), "allow", "block"
  block_status: 403          # HTTP status for blocked requests (default 403)
  block_body: ""             # optional response body
  block_content_type: "text/plain"  # Content-Type for block response
  expose_headers: false      # add X-AI-Crawler-* response headers
  policies:
    - crawler: GPTBot
      action: block
    - crawler: ClaudeBot
      action: block
    - crawler: CCBot
      action: block
      disallow_paths:
        - "/private/**"
        - "/admin/*"
    - crawler: PerplexityBot
      action: allow
      allow_paths:
        - "/public/**"
        - "/blog/**"
  custom_crawlers:
    - name: InternalBot
      pattern: "(?i)InternalBot"
```

### Per-Route

```yaml
routes:
  - id: api
    path: /api/
    ai_crawl_control:
      enabled: true
      default_action: block
```

Per-route config merges with global: route fields override global fields, with global values used as defaults for unset route fields.

### Zero-Config Mode

Enable with no policies to automatically monitor all known crawlers:

```yaml
ai_crawl_control:
  enabled: true
```

All 17 built-in crawlers will be detected and tracked in metrics with the default `monitor` action (requests pass through).

## Built-In Crawlers

| Name | Description |
|------|-------------|
| GPTBot | OpenAI training crawler |
| ChatGPT-User | OpenAI ChatGPT browsing |
| ClaudeBot | Anthropic training crawler |
| anthropic-ai | Anthropic AI agent |
| Google-Extended | Google AI training |
| CCBot | Common Crawl |
| Bytespider | ByteDance/TikTok |
| Applebot-Extended | Apple AI training |
| PerplexityBot | Perplexity AI search |
| Amazonbot | Amazon AI |
| FacebookBot | Meta AI |
| cohere-ai | Cohere AI |
| Diffbot | Diffbot extraction |
| Timpibot | Timpi search |
| ImagesiftBot | Imagesift AI |
| Omgilibot | Omgili data crawler |
| AI2Bot | Allen AI |

## Custom Crawlers

Define additional crawlers with regex patterns:

```yaml
ai_crawl_control:
  enabled: true
  custom_crawlers:
    - name: MyCompanyBot
      pattern: "(?i)MyCompanyBot"
  policies:
    - crawler: MyCompanyBot
      action: allow
```

Custom crawlers are checked before built-in crawlers. Patterns must be valid Go regular expressions.

## Path Enforcement

Policies support path-level control using doublestar glob patterns:

- `*` matches any characters within a single path segment (does not cross `/`)
- `**` matches across path segment boundaries

`allow_paths` and `disallow_paths` are mutually exclusive on the same policy.

**disallow_paths** — block the crawler on matching paths, allow elsewhere:

```yaml
policies:
  - crawler: GPTBot
    action: allow
    disallow_paths:
      - "/private/**"
      - "/admin/*"
```

**allow_paths** — only allow the crawler on matching paths, block elsewhere:

```yaml
policies:
  - crawler: CCBot
    action: allow
    allow_paths:
      - "/public/**"
      - "/blog/**"
```

## Response Headers

When `expose_headers: true`, the middleware sets response headers:

- `X-AI-Crawler-Detected: <name>` — on monitored requests
- `X-AI-Crawler-Blocked: <name>` — on blocked requests

These are off by default to avoid leaking classification information to crawlers.

## Custom Block Response

```yaml
ai_crawl_control:
  enabled: true
  default_action: block
  block_status: 451
  block_body: "Unavailable For Legal Reasons"
  block_content_type: "text/html"
```

When `block_body` is empty and `block_status` is 403 (default), the response uses the standard JSON error format (`{"code":403,"message":"Forbidden","details":"AI crawler blocked: <name>"}`).

## Middleware Position

Step 2.83 in the middleware chain — after `bot_detection` (2.8), before `ip_blocklist` (2.85).

## Admin API

`GET /ai-crawl-control` returns per-route statistics:

```json
{
  "route1": {
    "total_detected": 150,
    "total_blocked": 100,
    "total_allowed": 30,
    "total_monitored": 20,
    "crawlers": {
      "GPTBot": {
        "requests": 80,
        "blocked": 80,
        "allowed": 0,
        "monitored": 0,
        "last_seen": "2026-02-25T10:30:00Z",
        "action": "block"
      },
      "ClaudeBot": {
        "requests": 30,
        "blocked": 0,
        "allowed": 30,
        "monitored": 0,
        "last_seen": "2026-02-25T10:28:00Z",
        "action": "allow"
      }
    }
  }
}
```

## Validation Rules

- `default_action` must be `monitor`, `allow`, or `block` (default: `monitor`)
- `block_status` must be 100-599 (default: 403)
- Each policy must have a non-empty `crawler` and valid `action`
- `allow_paths` and `disallow_paths` are mutually exclusive on the same policy
- Custom crawler `name` must be non-empty and unique
- Custom crawler `pattern` must be a valid Go regex
