# AI Provider Reference

Details on how the AI gateway translates between the unified OpenAI-compatible format and each provider's native API.

## OpenAI

- **Endpoint**: `POST {base_url}/v1/chat/completions`
- **Auth**: `Authorization: Bearer {api_key}`
- **Format**: Near pass-through — the unified format IS the OpenAI format
- **Streaming**: SSE with `data: {json}\n\n` lines, terminated by `data: [DONE]\n\n`
- **Organization**: Set via `org_id` → `OpenAI-Organization` header

### Required Fields
- `api_key`

### Optional Fields
- `org_id` — OpenAI organization ID
- `base_url` — defaults to `https://api.openai.com`

## Anthropic

- **Endpoint**: `POST {base_url}/v1/messages`
- **Auth**: `x-api-key: {api_key}`, `anthropic-version: 2023-06-01`
- **Format**: Messages API with system message extraction

### Translation Details

**Request**:
- System messages are extracted from the messages array and sent as the top-level `system` field
- Remaining messages are sent as `messages` array
- `max_tokens` is required by Anthropic — defaults to 4096 if not provided
- `user` field maps to `metadata.user_id`

**Response**:
- `content[].text` blocks are concatenated into a single assistant message
- `stop_reason` is mapped: `end_turn` → `stop`, `max_tokens` → `length`
- `usage.input_tokens` / `output_tokens` map to `prompt_tokens` / `completion_tokens`

**Streaming**:
- Anthropic uses `event:` type lines: `message_start`, `content_block_start`, `content_block_delta`, `message_delta`, `message_stop`
- `content_block_delta` events with `text_delta` type are translated to OpenAI delta format
- `message_delta` carries final usage statistics
- `message_stop` signals stream end (mapped to `[DONE]`)
- `message_start`, `content_block_start`, `ping` events are skipped

### Required Fields
- `api_key`

### Optional Fields
- `base_url` — defaults to `https://api.anthropic.com`

## Azure OpenAI

- **Endpoint**: `POST {base_url}/openai/deployments/{deployment_id}/chat/completions?api-version={api_version}`
- **Auth**: `api-key: {api_key}`
- **Format**: OpenAI-compatible (same request/response format)
- **Streaming**: Same SSE format as OpenAI

### Required Fields
- `api_key`
- `base_url` — Azure resource URL (e.g., `https://myresource.openai.azure.com`)
- `deployment_id` — Azure deployment name
- `api_version` — API version (e.g., `2024-02-01`)

## Google Gemini

- **Endpoint**: `POST {base_url}/v1beta/models/{model}:generateContent` (non-streaming) or `streamGenerateContent?alt=sse` (streaming)
- **Auth**: `x-goog-api-key: {api_key}`
- **Format**: Contents/parts format translation

### Translation Details

**Request**:
- System messages are extracted into the `systemInstruction` field
- `assistant` role is mapped to `model` role
- Messages are converted to `contents[].parts[].text` format
- `max_tokens` maps to `generationConfig.maxOutputTokens`
- `temperature`, `top_p`, `stop` map to `generationConfig` fields

**Response**:
- `candidates[].content.parts[].text` are concatenated
- `model` role is mapped back to `assistant`
- `finishReason` is mapped: `STOP` → `stop`, `MAX_TOKENS` → `length`, `SAFETY` → `content_filter`
- `usageMetadata` maps to unified `usage` format

**Streaming**:
- Gemini streams as SSE with JSON objects matching the non-streaming response format
- Each event contains a `candidates` array with partial content
- Usage metadata may appear in events (tracked for final reporting)

### Required Fields
- `api_key`

### Optional Fields
- `base_url` — defaults to `https://generativelanguage.googleapis.com`
- `project_id` — GCP project ID (for Vertex AI usage)
- `region` — GCP region (for Vertex AI usage)

## Provider Error Mapping

All providers map errors to a consistent runway response:

| Provider HTTP Status | Runway HTTP Status | Error Type |
|---------------------|--------------------|----|
| 401, 403 | 502 Bad Gateway | `provider_auth_error` |
| 429 | 429 Too Many Requests | `rate_limit_exceeded` |
| 500, 503 | 502 Bad Gateway | `provider_error` |
| Network error | 502 Bad Gateway | `provider_error` |
| Timeout | 504 Gateway Timeout | `gateway_timeout` |
| Malformed response | 502 Bad Gateway | `provider_parse_error` |

For 429 responses, the `Retry-After` header from the provider is forwarded to the client.
