---
title: "Getting Started"
sidebar_position: 1
---

## Installation

Build from source (requires Go 1.25+):

```bash
go build -o runway ./cmd/runway/
```

Set version and build time at compile:

```bash
go build -ldflags "-X main.version=1.0.0 -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o runway ./cmd/runway/
```

## Docker

Build and run with Docker:

```bash
make docker-build
make docker-run
```

Start the full stack (runway + mock backends) with Compose:

```bash
make compose-up
```

Enable infrastructure services using profiles:

```bash
# Start with Redis (for distributed rate limiting / cache)
make compose-up-redis

# Start with OpenTelemetry collector (for tracing)
make compose-up-otel

# Start with all infrastructure
make compose-up-all
```

Build multi-architecture images (`linux/amd64` + `linux/arm64`) and push to a registry:

```bash
make docker-buildx
```

Stop everything:

```bash
make compose-down
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `configs/runway.yaml` | Path to configuration file |
| `-version` | — | Print version and build time, then exit |
| `-validate` | — | Validate configuration file and exit (non-zero on error) |

## Minimal Configuration

The smallest working config requires one listener and one route:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

routes:
  - id: "my-app"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://localhost:9000"
```

Start the runway:

```bash
./runway -config my-config.yaml
```

## Validating Configuration

Check your config for errors without starting the server:

```bash
./runway -validate -config my-config.yaml
# Prints "Configuration is valid" and exits 0, or prints error and exits 1
```

## Signal Handling

| Signal | Effect |
|--------|--------|
| `SIGHUP` | Reload configuration from disk (zero-downtime) |
| `SIGINT` / `SIGTERM` | Graceful shutdown |

Reload example:

```bash
kill -HUP $(pidof runway)
```

See [Admin API](../reference/admin-api.md) for HTTP-based reload via `POST /reload`.

## Environment Variable Expansion

YAML values support `${VAR}` syntax for environment variable substitution:

```yaml
authentication:
  jwt:
    secret: "${JWT_SECRET}"
  api_key:
    keys:
      - key: "${API_KEY_PROD}"
        client_id: "prod-client"
```

Unset variables are kept as-is in the config (e.g., `${MISSING_VAR}` remains literally `${MISSING_VAR}`).

## Default Settings

When not specified, the gateway applies these defaults:

- **Listener timeouts**: 30s read, 30s write, 60s idle
- **Registry**: `memory` type
- **API key header**: `X-API-Key`
- **JWT algorithm**: `HS256`
- **Log level**: `info`, output to `stdout`
- **Admin API**: Enabled on port 8081

## Next Steps

- [Core Concepts](core-concepts.md) — understand the request processing pipeline
- [Configuration Reference](../reference/configuration-reference.md) — full schema reference
- [Examples](examples.md) — production-ready configuration templates
