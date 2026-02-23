# Response Signing

The response signing middleware signs outbound responses so that downstream consumers can verify that the response was not tampered with in transit.

## Configuration

Per-route:

```yaml
routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://backend:9000"
    response_signing:
      enabled: true
      algorithm: "hmac-sha256"
      secret: "base64-encoded-secret-at-least-32-bytes"
      key_id: "gateway-key-1"
      header: "X-Response-Signature"
      include_headers:
        - "Content-Type"
        - "X-Request-ID"
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `response_signing.enabled` | bool | false | Enable response signing |
| `response_signing.algorithm` | string | hmac-sha256 | Signing algorithm |
| `response_signing.secret` | string | -- | Base64-encoded HMAC secret (min 32 decoded bytes) |
| `response_signing.key_file` | string | -- | Path to PEM-encoded RSA private key (for RSA algorithms) |
| `response_signing.key_id` | string | -- | Key identifier included in signature header |
| `response_signing.header` | string | X-Response-Signature | Response header name for the signature |
| `response_signing.include_headers` | []string | -- | Response headers to include in signature |

## Supported Algorithms

- `hmac-sha256` -- HMAC with SHA-256 (requires `secret`)
- `hmac-sha512` -- HMAC with SHA-512 (requires `secret`)
- `rsa-sha256` -- RSA PKCS#1 v1.5 with SHA-256 (requires `key_file`)

## Signature Header Format

The signature header value uses the format:

```
keyId="gateway-key-1",algorithm="hmac-sha256",signature="base64-encoded-signature"
```

The signature is computed over the response body concatenated with the values of `include_headers` (in order).

## Admin Endpoint

`GET /response-signing` returns per-route response signing statistics.

```bash
curl http://localhost:8081/response-signing
```

See [Configuration Reference](configuration-reference.md#response-signing-per-route) for field details.
