# Runway

A high-performance API runway and reverse proxy for HTTP, TCP, and UDP with 90+ built-in middleware features.

Runway provides enterprise-grade traffic management, security, resilience, and observability out of the box through declarative YAML configuration. No plugins, no scripting languages, no external dependencies required — just a single binary and a config file.

## Highlights

- **Multi-protocol** — HTTP/HTTPS reverse proxy, TCP/UDP L4 proxy, WebSocket, SSE, gRPC (native + Web), HTTP-to-Thrift, HTTP/3 QUIC, HTTP CONNECT tunneling
- **Backend integrations** — Static backends, Consul/etcd/Kubernetes service discovery, AWS Lambda, AMQP/RabbitMQ, Pub/Sub (GCP, AWS, NATS, Kafka), FastCGI
- **Advanced load balancing** — Round-robin, least-connections, consistent-hash, least-response-time, weighted splits
- **Built-in security** — JWT/JWKS, OAuth 2.0/OIDC, mTLS, API keys, WAF (Coraza), CSRF, OPA, IP/geo filtering, PII redaction, field encryption, WASM plugins
- **Resilience** — Retries with budgets, hedging, circuit breakers, adaptive concurrency, outlier detection, load shedding, backpressure, SLO enforcement
- **Traffic control** — Rate limiting, spike arrest, quotas, throttling, bandwidth limits, priority queues, consumer groups, multi-tenancy, canary/blue-green/A/B
- **Observability** — Prometheus metrics, OpenTelemetry tracing, structured logging (zap), event webhooks, audit logging, developer portal
- **AI Gateway** — Native proxy for OpenAI, Anthropic, Azure OpenAI, and Gemini with prompt guard, token rate limiting, and response decoration
- **Kubernetes native** — Ingress Controller supporting both Ingress v1 and Gateway API, plus hybrid CP/DP cluster mode with mTLS gRPC config streaming
- **Zero-downtime ops** — Hot config reload via SIGHUP or admin API, graceful shutdown with connection draining, schema evolution validation
- **Extensible** — Public Go module API for custom middleware, Lua scripting, WASM plugins, OPA policies
- **Single binary** — No runtime dependencies. Add Redis for distributed features, or run fully standalone

## Quick Start

### Install from source

```bash
# Requires Go 1.25+
git clone https://github.com/wudi/runway.git
cd runway
make build
```

### Run

```bash
./build/runway -config configs/runway.yaml
```

### Docker

```bash
docker run -p 8080:8080 -p 8081:8081 \
  -v $(pwd)/configs:/app/configs:ro \
  ghcr.io/wudi/runway:latest
```

Multi-arch images (`linux/amd64`, `linux/arm64`) are published to `ghcr.io/wudi/runway`. Tags follow semver on release (e.g. `v1.2.3`, `1.2`) and `latest` + `sha-<commit>` on every push to master.

### Docker Compose

```bash
# Runway + mock backends
make compose-up

# With Redis (distributed rate limiting & caching)
make compose-up-redis

# With OpenTelemetry collector
make compose-up-otel

# Full stack (Redis + OTEL + Consul + etcd)
make compose-up-all
```

## Minimal Configuration

```yaml
listeners:
  - address: ":8080"
    protocol: http

routes:
  - id: my-api
    path: /api/
    path_prefix: true
    backends:
      - url: http://localhost:9001
```

## Configuration Example

```yaml
listeners:
  - address: ":8080"
    protocol: http
    http:
      read_timeout: 30s
      write_timeout: 30s

registry:
  type: consul
  consul:
    address: "localhost:8500"

authentication:
  jwt:
    enabled: true
    secret: "${JWT_SECRET}"
    issuer: "https://auth.example.com"

routes:
  - id: users-api
    path: /api/v1/users
    path_prefix: true
    service:
      name: users-service
      tags: [production]
    auth:
      required: true
      methods: [jwt]
    rate_limit:
      rate: 100
      period: 1m
      burst: 10
    retry_policy:
      max_retries: 2
      budget:
        ratio: 0.1
    circuit_breaker:
      max_requests: 5
      timeout: 30s
      failure_threshold: 3
    cache:
      enabled: true
      ttl: 60s
    timeout_policy:
      request: 30s
      backend: 10s
    transform:
      request:
        headers:
          add:
            X-Request-ID: "$request_id"
            X-Forwarded-For: "$remote_addr"
```

## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │              Listeners                   │
                    │      HTTP / HTTPS / TCP / UDP / H3      │
                    └────────────────┬────────────────────────┘
                                     │
                    ┌────────────────▼────────────────────────┐
                    │          Global Middleware               │
                    │  Recovery → RealIP → HTTPS Redirect     │
                    │  → Allowed Hosts → RequestID → mTLS     │
                    │  → Tracing → Logging                    │
                    └────────────────┬────────────────────────┘
                                     │
                    ┌────────────────▼────────────────────────┐
                    │           Route Matching                 │
                    └────────────────┬────────────────────────┘
                                     │
                    ┌────────────────▼────────────────────────┐
                    │       Per-Route Middleware Chain         │
                    │                                         │
                    │  IP Filter → Geo → Bot → CORS → Rate   │
                    │  Limit → Spike Arrest → Quota →         │
                    │  Throttle → Auth → OPA → CSRF →         │
                    │  Priority → Rules → WAF → Validation    │
                    │  → Cache → Circuit Breaker → Adaptive   │
                    │  Concurrency → Compression → ETag       │
                    │  → Transform → Mirror → ...             │
                    └────────────────┬────────────────────────┘
                                     │
              ┌──────────────────────┼──────────────────────┐
              │                      │                      │
     ┌────────▼──────┐    ┌─────────▼────────┐   ┌────────▼───────┐
     │  HTTP Proxy   │    │  WebSocket / SSE  │   │ gRPC / Thrift  │
     │  + Retries    │    │     Proxy         │   │  / AI Gateway  │
     │  + Hedging    │    │                   │   │                │
     └────────┬──────┘    └─────────┬────────┘   └────────┬───────┘
              │                     │                      │
     ┌────────▼─────────────────────▼──────────────────────▼───────┐
     │                    Load Balancer                             │
     │  Round-Robin │ Least-Conn │ Consistent-Hash │ Weighted │ …  │
     └────────┬─────────────────────┬──────────────────────┬───────┘
              │                     │                      │
     ┌────────▼──────┐    ┌────────▼───────┐    ┌─────────▼──────┐
     │   Backend 1   │    │   Backend 2    │    │   Backend N    │
     └───────────────┘    └────────────────┘    └────────────────┘
```

## CLI

```
Usage: runway [flags]

Flags:
  -config string    Path to configuration file (default "configs/runway.yaml")
  -validate         Validate configuration and exit
  -version          Print version and exit
```

Signals:
- `SIGHUP` — reload configuration without downtime
- `SIGINT` / `SIGTERM` — graceful shutdown

## Documentation

Full documentation is available in the [docs/](docs/) directory:

- [Getting Started](docs/getting-started/getting-started.md) — Installation, CLI flags, minimal config
- [Core Concepts](docs/getting-started/core-concepts.md) — Listeners, routes, backends, request pipeline
- [Configuration Reference](docs/reference/configuration-reference.md) — Complete YAML schema
- [Examples](docs/getting-started/examples.md) — Production-ready configuration templates
- [Admin API Reference](docs/reference/admin-api.md) — Health, feature endpoints, dashboard, reload
- [Security](docs/security/security.md) — Authentication, WAF, IP filtering, mTLS, OPA
- [Resilience](docs/resilience/resilience.md) — Retries, circuit breakers, timeouts, load shedding
- [Traffic Management](docs/traffic-routing/traffic-management.md) — Canary, blue-green, A/B, mirroring
- [Rate Limiting](docs/rate-limiting/rate-limiting-and-throttling.md) — Rate limits, throttle, quotas, bandwidth
- [Caching](docs/caching/caching.md) — Response caching, coalescing, ETags
- [Protocol Translation](docs/protocol/protocol-translation.md) — gRPC, WebSocket, SSE, GraphQL, HTTP/3
- [AI Gateway](docs/ai-gateway/ai-gateway.md) — OpenAI, Anthropic, Azure, Gemini proxy
- [Kubernetes Ingress](docs/traffic-routing/kubernetes-ingress.md) — Ingress v1 and Gateway API controller
- [Cluster Mode](docs/reference/cluster-mode.md) — CP/DP hybrid deployment with mTLS gRPC
- [Extensibility](docs/reference/extensibility.md) — Public Go module API, Lua, WASM plugins

## Development

```bash
make deps           # Install dependencies
make build          # Build binary
make test           # Run tests
make test-coverage  # Run tests with coverage
make fmt            # Format code
make lint           # Lint code
make docker-buildx  # Multi-arch Docker image
```

## License

MIT
