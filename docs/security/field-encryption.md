---
title: "Payload Field-Level Encryption"
sidebar_position: 15
---

The field encryption middleware provides AES-GCM-256 encryption and decryption of specific JSON fields in request and response bodies. This allows sensitive data to be encrypted at the gateway layer before reaching backends, and decrypted on the way back to the client, without requiring application-level encryption logic.

## How It Works

1. On **request**: The middleware parses the JSON body, locates fields specified in `request_fields` using gjson paths, encrypts their values with AES-GCM-256, replaces them with the encoded ciphertext, and forwards the modified body to the backend
2. On **response**: The middleware parses the JSON body, locates fields specified in `response_fields`, decrypts their ciphertext values, and returns the plaintext to the client
3. Each encryption operation generates a fresh 12-byte nonce from `crypto/rand`, which is prepended to the ciphertext before encoding
4. Non-string field values are JSON-serialized before encryption and deserialized after decryption

## Configuration

Field encryption is per-route only.

```yaml
routes:
  - id: user-api
    path: /api/users
    path_prefix: true
    backends:
      - url: http://user-service:8080
    field_encryption:
      enabled: true
      key: "${FIELD_ENCRYPTION_KEY}"     # base64-encoded 32-byte key
      encoding: base64                    # base64 (default) or hex
      request_fields:                     # fields to encrypt before forwarding
        - "ssn"
        - "credit_card.number"
        - "payment.details.account"
      response_fields:                    # fields to decrypt before returning
        - "ssn"
        - "credit_card.number"
        - "payment.details.account"
```

### Full Example

```yaml
routes:
  - id: customer-api
    path: /api/customers
    path_prefix: true
    backends:
      - url: http://customer-service:8080
    field_encryption:
      enabled: true
      key: "${ENCRYPTION_KEY}"
      encoding: base64
      request_fields:
        - "data.ssn"
        - "data.tax_id"
        - "data.bank_account"
      response_fields:
        - "data.ssn"
        - "data.tax_id"
        - "data.bank_account"

  - id: payment-api
    path: /api/payments
    path_prefix: true
    backends:
      - url: http://payment-service:8080
    field_encryption:
      enabled: true
      key: "${PAYMENT_ENCRYPTION_KEY}"
      encoding: hex
      request_fields:
        - "card.number"
        - "card.cvv"
      response_fields: []                # backend stores encrypted; no decryption needed
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable field-level encryption |
| `key` | string | - | Base64-encoded 32-byte AES-256 key |
| `encoding` | string | `base64` | Ciphertext encoding: `base64` or `hex` |
| `request_fields` | list | - | gjson paths of fields to encrypt in request bodies |
| `response_fields` | list | - | gjson paths of fields to decrypt in response bodies |

## Key Requirements

The encryption key must be:

- **Base64-encoded** in the config (use environment variable substitution: `key: "${ENCRYPTION_KEY}"`)
- **Exactly 32 bytes** when decoded (256 bits for AES-256)
- **Cryptographically random** — generate with `openssl rand -base64 32` or equivalent

```bash
# Generate a suitable key
openssl rand -base64 32
# Example output: K7gNU3sdo+OL0wNhqoVWhr3g6s1xYv72ol/pe/Unols=
```

## Field Path Syntax

Fields are specified using gjson path syntax, which supports:

- Simple keys: `"ssn"`
- Nested objects: `"data.personal.ssn"`
- Array elements: `"users.0.email"`
- Wildcards: `"users.#.email"` (encrypts email in all array elements)

```yaml
request_fields:
  - "ssn"                          # top-level field
  - "address.postal_code"          # nested field
  - "dependents.#.ssn"             # all SSN fields in array
```

## Encryption Details

### Algorithm

AES-GCM-256 (Galois/Counter Mode) provides both confidentiality and integrity. Each encrypted value is authenticated — any tampering with the ciphertext will cause decryption to fail.

### Nonce

A 12-byte nonce is generated from `crypto/rand` for every encryption operation. The nonce is prepended to the AES-GCM ciphertext before encoding:

```
encoded_value = encode(nonce || ciphertext || tag)
```

Where `encode` is base64 or hex, depending on the `encoding` setting.

### Encoding

- **`base64`** (default) — Standard base64 encoding. Produces shorter output, suitable for JSON string values.
- **`hex`** — Hexadecimal encoding. Produces longer output but uses only alphanumeric characters.

### Example Transformation

Request body before encryption (with `request_fields: ["ssn"]`):

```json
{
  "name": "Jane Doe",
  "ssn": "123-45-6789"
}
```

Request body after encryption:

```json
{
  "name": "Jane Doe",
  "ssn": "dGhpcyBpcyBhIGJhc2U2NCBlbmNvZGVkIGNpcGhlcnRleHQ="
}
```

## Middleware Position

Step **8.6** in the per-route middleware chain -- after bandwidth limiting (step 8.5) and before request validation (step 9). This position ensures that:

- Body size limits are enforced before encryption processing
- Bandwidth limits apply to the wire-format body
- Validation runs after encryption, so validators see the encrypted field values (not plaintext)
- WebSocket, cache, and circuit breaker operate on the encrypted body

## Mutual Exclusivity

Field encryption is mutually exclusive with passthrough mode. A route cannot have both `passthrough: true` and `field_encryption.enabled: true` -- this is a config validation error. Passthrough mode skips body processing entirely.

## Validation Rules

- `key` is required when `enabled: true`
- `key` must be valid base64 and decode to exactly 32 bytes
- `encoding` must be `base64` or `hex`
- At least one of `request_fields` or `response_fields` must be non-empty when `enabled: true`
- Field paths must be non-empty strings
- Field encryption is mutually exclusive with `passthrough: true`

## Admin API

### GET `/field-encryption`

Returns per-route field encryption status and operation metrics.

```bash
curl http://localhost:8081/field-encryption
```

**Response:**
```json
{
  "customer-api": {
    "encoding": "base64",
    "request_fields": ["data.ssn", "data.tax_id", "data.bank_account"],
    "response_fields": ["data.ssn", "data.tax_id", "data.bank_account"],
    "total_requests_encrypted": 4200,
    "total_responses_decrypted": 3800,
    "fields_encrypted": 12600,
    "fields_decrypted": 11400,
    "encryption_errors": 0,
    "decryption_errors": 3
  },
  "payment-api": {
    "encoding": "hex",
    "request_fields": ["card.number", "card.cvv"],
    "response_fields": [],
    "total_requests_encrypted": 1500,
    "total_responses_decrypted": 0,
    "fields_encrypted": 3000,
    "fields_decrypted": 0,
    "encryption_errors": 0,
    "decryption_errors": 0
  }
}
```

## Error Handling

- If a `request_field` path does not exist in the request body, it is silently skipped (the field is not required to be present)
- If a `response_field` contains a value that cannot be decrypted (corrupted ciphertext, wrong key), the field value is left unchanged and a warning is logged. The response is still returned to the client.
- If the request or response body is not valid JSON, the middleware passes it through unmodified and logs a warning

## Notes

- The middleware buffers the full request and response bodies in memory for JSON manipulation. Combine with `body_limit` to cap maximum body size.
- Field encryption uses `tidwall/gjson` for path lookups and `tidwall/sjson` for value replacement, consistent with the existing body transform middleware.
- Key rotation requires a config reload with the new key. During rotation, responses encrypted with the old key will fail to decrypt. Coordinate key rotation with a brief maintenance window or implement application-level key versioning.
- The nonce is unique per encryption operation. Even identical plaintext values produce different ciphertext on each request, preventing ciphertext correlation.
- For regex-based PII masking (irreversible), see [PII Redaction](pii-redaction.md). Field encryption is reversible -- the original value can be recovered with the correct key.

See [Transformations](../transformations/transformations.md) for body transform configuration using gjson/sjson.
See [Configuration Reference](../reference/configuration-reference.md) for all fields.
