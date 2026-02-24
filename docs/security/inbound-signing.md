---
title: "Inbound Request Signature Verification"
sidebar_position: 14
---

The inbound signing middleware verifies HMAC signatures on incoming requests, ensuring that requests originate from trusted clients who possess a shared secret. This is commonly used for webhook receivers, service-to-service authentication, and API integrations where requests must be provably authentic and tamper-proof.

## How It Works

1. The client computes an HMAC signature over a canonical signing string that includes the HTTP method, URI, timestamp, and body hash
2. The client sends the signature, timestamp, and key ID in request headers
3. The gateway reconstructs the signing string from the incoming request and verifies the HMAC
4. If the signature is invalid, the timestamp is outside the allowed clock skew, or required headers are missing, the request is rejected with 401
5. In shadow mode, failures are logged but requests are allowed through

## Configuration

### Global

```yaml
inbound_signing:
  enabled: true
  algorithm: hmac-sha256                # hmac-sha256 or hmac-sha512
  secret: "${INBOUND_SIGNING_SECRET}"   # base64-encoded, minimum 32 bytes decoded
  header_prefix: "X-Signature-"         # default: "X-Signature-"
  max_clock_skew: 5m                    # default: 5m
  shadow_mode: false                    # log failures without rejecting
  extra_headers:                        # additional headers included in signing string
    - "Content-Type"
    - "X-Request-Id"
```

### Per-Route

Per-route config overrides the global config. Per-route non-zero fields take precedence:

```yaml
routes:
  - id: webhook-receiver
    path: /webhooks
    path_prefix: true
    backends:
      - url: http://webhook-handler:8080
    inbound_signing:
      enabled: true
      algorithm: hmac-sha512
      secret: "${WEBHOOK_SECRET}"
      max_clock_skew: 2m
      extra_headers:
        - "Content-Type"

  - id: partner-api
    path: /partner/v1
    path_prefix: true
    backends:
      - url: http://partner-service:8080
    inbound_signing:
      enabled: true
      secret: "${PARTNER_SECRET}"
      # inherits global algorithm, header_prefix, max_clock_skew
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable signature verification |
| `algorithm` | string | `hmac-sha256` | Signing algorithm: `hmac-sha256` or `hmac-sha512` |
| `secret` | string | - | Base64-encoded shared secret (decoded must be >= 32 bytes) |
| `header_prefix` | string | `X-Signature-` | Prefix for signature-related headers |
| `max_clock_skew` | duration | `5m` | Maximum allowed difference between request timestamp and server time |
| `shadow_mode` | bool | `false` | Log verification failures without rejecting requests |
| `extra_headers` | list | - | Additional request headers to include in the signing string |

## Signature Headers

The client must send three headers using the configured prefix:

| Header | Example | Description |
|--------|---------|-------------|
| `{prefix}Timestamp` | `X-Signature-Timestamp: 1708444800` | Unix timestamp (seconds) when the request was signed |
| `{prefix}Signature` | `X-Signature-Signature: a1b2c3...` | Hex-encoded HMAC signature |
| `{prefix}Key-ID` | `X-Signature-Key-ID: partner-prod` | Identifier for the signing key (informational, included in logs) |

## Signing String

The canonical signing string is constructed as follows:

```
METHOD\n
URI\n
TIMESTAMP\n
SHA256(BODY)\n
header:value\n
header:value
```

Each component:

- **METHOD** — HTTP method in uppercase (e.g., `POST`)
- **URI** — Full request URI including query string (e.g., `/webhooks/payment?id=123`)
- **TIMESTAMP** — The value from the `{prefix}Timestamp` header
- **SHA256(BODY)** — Hex-encoded SHA-256 hash of the raw request body (empty string hashed if no body)
- **header:value** — Extra headers listed in `extra_headers`, lowercased header name, colon, trimmed value, one per line, in the order specified in config

The HMAC is computed over this signing string using the configured algorithm and the decoded secret.

### Client Example (Python)

```python
import hmac, hashlib, base64, time, requests

secret = base64.b64decode("your-base64-secret-here")
body = '{"event": "payment.completed", "id": "pay_123"}'
timestamp = str(int(time.time()))
body_hash = hashlib.sha256(body.encode()).hexdigest()

signing_string = f"POST\n/webhooks/payment\n{timestamp}\n{body_hash}"
signature = hmac.new(secret, signing_string.encode(), hashlib.sha256).hexdigest()

requests.post("https://gateway.example.com/webhooks/payment",
    headers={
        "X-Signature-Timestamp": timestamp,
        "X-Signature-Signature": signature,
        "X-Signature-Key-ID": "partner-prod",
        "Content-Type": "application/json",
    },
    data=body)
```

## Shadow Mode

Set `shadow_mode: true` to monitor signature verification without rejecting requests. This is useful for gradual rollout:

1. Deploy with `shadow_mode: true`
2. Monitor logs for verification failures (logged at WARN level with reason, key ID, and route)
3. Coordinate with clients to ensure all requests are properly signed
4. Set `shadow_mode: false` to start enforcing

Shadow mode still validates and logs all checks — it just does not return 401.

## Middleware Position

Step **6.37** in the per-route middleware chain — after CSRF protection (step 6.35) and before idempotency key support (step 6.4). This position ensures that authentication context is available and that unsigned requests are rejected before they consume idempotency keys or enter the proxy pipeline.

## Error Responses

| Status | Condition |
|--------|-----------|
| 401 | Missing `{prefix}Timestamp` header |
| 401 | Missing `{prefix}Signature` header |
| 401 | Timestamp outside `max_clock_skew` window |
| 401 | Signature does not match computed HMAC |

All 401 responses include a JSON body with a `reason` field:

```json
{
  "error": "signature verification failed",
  "reason": "timestamp outside allowed clock skew"
}
```

## Validation Rules

- `secret` is required when `enabled: true`
- `secret` must be valid base64 and decode to at least 32 bytes
- `algorithm` must be `hmac-sha256` or `hmac-sha512`
- `max_clock_skew` must be >= 0
- `extra_headers` entries must not be empty strings

## Admin API

### GET `/inbound-signing`

Returns per-route inbound signing status and verification metrics.

```bash
curl http://localhost:8081/inbound-signing
```

**Response:**
```json
{
  "webhook-receiver": {
    "algorithm": "hmac-sha512",
    "header_prefix": "X-Signature-",
    "max_clock_skew": "2m0s",
    "shadow_mode": false,
    "total_requests": 5000,
    "verified": 4980,
    "failed": 20,
    "missing_timestamp": 3,
    "missing_signature": 5,
    "clock_skew_rejected": 7,
    "invalid_signature": 5,
    "shadow_failures": 0
  },
  "partner-api": {
    "algorithm": "hmac-sha256",
    "header_prefix": "X-Signature-",
    "max_clock_skew": "5m0s",
    "shadow_mode": false,
    "total_requests": 12000,
    "verified": 12000,
    "failed": 0,
    "missing_timestamp": 0,
    "missing_signature": 0,
    "clock_skew_rejected": 0,
    "invalid_signature": 0,
    "shadow_failures": 0
  }
}
```

## Notes

- The body is fully read and buffered for hashing. For large request bodies, consider combining with `body_limit` to cap the maximum size.
- Clock skew validation uses the gateway's system clock. Ensure NTP synchronization on both client and gateway hosts.
- The `Key-ID` header is not used for verification — it is logged for auditing and debugging. Key rotation is handled by updating the `secret` value and reloading config.
- Inbound signing is separate from [Backend Signing](security.md), which signs outgoing requests to backends. Both can be active on the same route.

See [CSRF Protection](csrf.md) for the preceding middleware step.
See [Configuration Reference](../reference/configuration-reference.md) for all fields.
