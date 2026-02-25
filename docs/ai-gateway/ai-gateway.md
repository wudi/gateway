# AI Gateway

The AI Gateway provides a unified proxy for Large Language Model (LLM) APIs. It accepts OpenAI-compatible chat completion requests and translates them to/from four providers: **OpenAI**, **Anthropic**, **Azure OpenAI**, and **Google Gemini**.

## Features

- **Unified API**: Accept OpenAI-format requests, regardless of the backend provider
- **Provider translation**: Automatic format conversion for Anthropic, Azure, and Gemini
- **Streaming support**: Server-Sent Events (SSE) with per-event flushing
- **Model mapping**: Map client model names to provider models
- **Prompt guard**: Block or log prompt injection attempts with regex patterns
- **Prompt decorator**: Prepend/append system messages to every request
- **Token rate limiting**: Sliding window token budgets with word-count estimation
- **Observability**: Per-route stats, token counting, latency tracking

## Quick Start

```yaml
routes:
  - id: chat
    path: /v1/chat/completions
    methods: [POST]
    ai:
      enabled: true
      provider: openai
      model: gpt-4o
      api_key: ${OPENAI_API_KEY}
```

Send a request:

```bash
curl -X POST http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## Configuration Reference

### `ai` (per-route)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable AI gateway for this route |
| `provider` | string | required | `openai`, `anthropic`, `azure_openai`, or `gemini` |
| `model` | string | — | Default model name |
| `model_mapping` | map[string]string | — | Map client model names to provider models |
| `api_key` | string | required | Provider API key (supports `${ENV_VAR}`) |
| `base_url` | string | provider default | Override provider base URL |
| `api_version` | string | — | Azure: required API version |
| `deployment_id` | string | — | Azure: required deployment ID |
| `project_id` | string | — | Gemini: GCP project ID |
| `region` | string | — | Gemini: GCP region |
| `org_id` | string | — | OpenAI: organization ID |
| `timeout` | duration | `60s` | Per-request timeout |
| `max_tokens` | int | — | Cap on max_tokens (overrides client if larger) |
| `temperature` | float | — | Override temperature (nil = use client value) |
| `stream_default` | bool | `false` | Stream by default if client omits |
| `pass_headers` | []string | — | Forward these headers from client to provider |
| `idle_timeout` | duration | `30s` | SSE idle timeout per event |
| `max_body_size` | int64 | `10485760` | Max request body size (10MB) |

### `ai.prompt_guard`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `deny_patterns` | []string | — | Regex patterns to block |
| `allow_patterns` | []string | — | Regex patterns that override deny |
| `deny_action` | string | `block` | `block` (return 400) or `log` (warn and pass) |
| `max_prompt_len` | int | `0` | Max total prompt length in characters (0 = unlimited) |

### `ai.prompt_decorate`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `prepend` | []message | — | Messages to prepend before user messages |
| `append` | []message | — | Messages to append after user messages |

Each message has `role` (`system`, `user`, `assistant`) and `content` (string).

### `ai.rate_limit`

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `tokens_per_minute` | int64 | `0` | Max tokens per minute (0 = unlimited) |
| `tokens_per_day` | int64 | `0` | Max tokens per day (0 = unlimited) |
| `key` | string | `ip` | Rate limit key: `ip`, `client_id`, `header:<name>`, `cookie:<name>`, `jwt_claim:<name>` |

## Provider Examples

### OpenAI

```yaml
ai:
  enabled: true
  provider: openai
  model: gpt-4o
  api_key: ${OPENAI_API_KEY}
  org_id: ${OPENAI_ORG_ID}
  max_tokens: 4096
```

### Anthropic

```yaml
ai:
  enabled: true
  provider: anthropic
  model: claude-sonnet-4-6-20250514
  api_key: ${ANTHROPIC_API_KEY}
  max_tokens: 4096
```

### Azure OpenAI

```yaml
ai:
  enabled: true
  provider: azure_openai
  model: gpt-4
  api_key: ${AZURE_OPENAI_KEY}
  base_url: https://myresource.openai.azure.com
  deployment_id: my-gpt4-deployment
  api_version: "2024-02-01"
```

### Google Gemini

```yaml
ai:
  enabled: true
  provider: gemini
  model: gemini-pro
  api_key: ${GEMINI_API_KEY}
```

## Model Mapping

Map client-facing model names to provider-specific models:

```yaml
ai:
  enabled: true
  provider: openai
  model: gpt-4o
  api_key: ${OPENAI_API_KEY}
  model_mapping:
    "gpt-4": "gpt-4-turbo"
    "cheap": "gpt-4o-mini"
    "best": "gpt-4o"
```

When a client requests `"model": "cheap"`, the gateway sends `"model": "gpt-4o-mini"` to the provider.

## Prompt Guard

Protect against prompt injection attacks:

```yaml
ai:
  enabled: true
  provider: openai
  model: gpt-4o
  api_key: ${OPENAI_API_KEY}
  prompt_guard:
    deny_patterns:
      - "(?i)ignore previous instructions"
      - "(?i)you are now"
      - "(?i)system prompt"
    allow_patterns:
      - "(?i)please ignore the noise"
    deny_action: block
    max_prompt_len: 50000
```

- **Deny patterns** are checked against the concatenated text of all messages
- **Allow patterns** override deny matches (useful for false positive exceptions)
- **deny_action**: `block` returns 400, `log` warns and passes through

## Prompt Decorator

Inject system messages into every request:

```yaml
ai:
  enabled: true
  provider: openai
  model: gpt-4o
  api_key: ${OPENAI_API_KEY}
  prompt_decorate:
    prepend:
      - role: system
        content: "You are a helpful customer support agent for Acme Corp."
      - role: system
        content: "Always be polite and concise."
    append:
      - role: system
        content: "Remember to suggest relevant documentation links."
```

## Token Rate Limiting

Enforce token budgets per client:

```yaml
ai:
  enabled: true
  provider: openai
  model: gpt-4o
  api_key: ${OPENAI_API_KEY}
  rate_limit:
    tokens_per_minute: 100000
    tokens_per_day: 1000000
    key: client_id
```

**How it works**:
1. Before the request, tokens are estimated using a word-count heuristic (`words * 1.3`)
2. The estimate is checked against the sliding window budget
3. If over budget, returns 429 with `Retry-After` header
4. After the response, actual token count from the provider replaces the estimate

## Error Handling

All errors use a consistent JSON format:

```json
{
  "error": {
    "type": "provider_auth_error",
    "message": "...",
    "provider": "openai"
  }
}
```

Error mapping:
| Provider Status | Gateway Status | Error Type |
|----------------|---------------|------------|
| 401, 403 | 502 Bad Gateway | `provider_auth_error` |
| 429 | 429 Too Many Requests | `rate_limit_exceeded` |
| 500, 503 | 502 Bad Gateway | `provider_error` |
| Network/timeout | 504 Gateway Timeout | `gateway_timeout` |
| Parse error | 502 Bad Gateway | `provider_parse_error` |

## Streaming

When the client sends `"stream": true`, the gateway:

1. Sends the request to the provider with streaming enabled
2. Sets response headers: `Content-Type: text/event-stream`, `Cache-Control: no-store`
3. Translates each provider SSE event to OpenAI-compatible format
4. Flushes each event individually for real-time delivery
5. Sends `data: [DONE]\n\n` when the stream ends

If no event arrives within `idle_timeout` (default 30s), the connection is closed with an error event.

## Response Headers

| Header | When | Description |
|--------|------|-------------|
| `X-AI-Provider` | Always | Provider name |
| `X-AI-Model` | Always | Model name (after mapping) |
| `X-AI-Tokens-Input` | Non-streaming | Input token count |
| `X-AI-Tokens-Output` | Non-streaming | Output token count |
| `X-AI-Tokens-Total` | Non-streaming | Total token count |

## Admin API

`GET /admin/ai` returns per-route statistics:

```json
{
  "route-1": {
    "provider": "openai",
    "model": "gpt-4o",
    "total_requests": 1523,
    "streaming_requests": 1200,
    "non_streaming_requests": 323,
    "total_tokens_in": 450000,
    "total_tokens_out": 230000,
    "total_errors": 12,
    "latency_sum_ms": 1282366
  }
}
```

Average latency can be computed as `latency_sum_ms / total_requests`.

## Security

- **API keys**: Always use environment variable references (`${ENV_VAR}`) — keys are never exposed in admin stats
- **Body size**: Requests are limited to `max_body_size` (default 10MB) to prevent abuse
- **Prompt guard**: Use deny patterns to block known injection techniques
- The AI route is mutually exclusive with backends, echo, static, and other innermost handlers
