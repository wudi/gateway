# SDK Generation

The gateway can auto-generate client SDKs from OpenAPI specs registered in the API catalog. Developers can download ready-to-use HTTP clients in Go, Python, or TypeScript directly from the admin port.

## Configuration

SDK generation is configured under the catalog section:

```yaml
admin:
  catalog:
    enabled: true
    title: "My API Gateway"
    sdk:
      enabled: true
      languages: [go, python, typescript]
      cache_ttl: 1h
```

## Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable SDK generation endpoints |
| `languages` | string[] | `[]` | Supported languages: `go`, `python`, `typescript` |
| `cache_ttl` | duration | `1h` | How long to cache generated SDKs |

## How It Works

1. The SDK generator walks the OpenAPI spec to extract endpoints, parameters, request/response types.
2. It renders language-specific templates to produce a functional HTTP client.
3. The client code is packaged as a `.zip` file.
4. Results are cached by `specID:language:specHash` with the configured TTL.

### Generated Clients

**Go**: Uses `net/http` with typed request/response structs. Each operation becomes a method on a `Client` struct.

**Python**: Uses the `requests` library with `dataclass` response types. Each operation becomes a method on a `Client` class.

**TypeScript**: Uses the `fetch` API with TypeScript interfaces. Each operation becomes an async method on a `Client` class.

## Endpoints

All SDK endpoints are served on the admin port under the catalog path.

### `GET /catalog/sdk`

Lists all specs with available SDK languages:

```bash
curl http://localhost:8081/catalog/sdk
```

**Response:**

```json
{
  "specs": [
    {
      "id": "specs-users-yaml",
      "title": "Users API",
      "version": "1.0.0",
      "languages": ["go", "python", "typescript"]
    }
  ]
}
```

### `GET /catalog/sdk/{specID}`

Lists available languages for a specific spec:

```bash
curl http://localhost:8081/catalog/sdk/specs-users-yaml
```

**Response:**

```json
{
  "spec_id": "specs-users-yaml",
  "title": "Users API",
  "version": "1.0.0",
  "languages": ["go", "python", "typescript"]
}
```

Returns `404` if the spec ID does not exist.

### `GET /catalog/sdk/{specID}/{language}`

Downloads the generated SDK as a zip file:

```bash
curl -o users-sdk-go.zip http://localhost:8081/catalog/sdk/specs-users-yaml/go
```

**Response Headers:**

```
Content-Type: application/zip
Content-Disposition: attachment; filename="specs-users-yaml-go-sdk.zip"
```

Returns `404` if the spec or language is not found. Returns `500` if generation fails.

## Validation

- `sdk.enabled` requires `catalog.enabled`
- `languages` must be a subset of `["go", "python", "typescript"]`

## Example

```yaml
admin:
  catalog:
    enabled: true
    title: "Payment Gateway"
    sdk:
      enabled: true
      languages: [go, typescript]
      cache_ttl: 30m

routes:
  - id: payments
    path: /api/payments/*
    openapi:
      spec_file: specs/payments.yaml
    backends:
      - url: http://payments-backend:8080
```

With this configuration:

```bash
# List available SDKs
curl http://localhost:8081/catalog/sdk

# Download Go SDK
curl -o payments-go.zip http://localhost:8081/catalog/sdk/specs-payments-yaml/go

# Download TypeScript SDK
curl -o payments-ts.zip http://localhost:8081/catalog/sdk/specs-payments-yaml/typescript
```

## See Also

- [Developer Portal](developer-portal.md) — API catalog and documentation UI
- [Configuration Reference](configuration-reference.md#admin) — Catalog config fields
- [Admin API Reference](admin-api.md#sdk-generation) — Admin endpoint reference
