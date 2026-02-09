# Gateway

A high-performance API gateway and reverse proxy for HTTP, TCP, and UDP with 30+ built-in middleware features.

Gateway provides enterprise-grade traffic management, security, resilience, and observability out of the box through declarative YAML configuration. No plugins, no scripting languages, no external dependencies required — just a single binary and a config file.

## Highlights

- **Multi-protocol** — HTTP/HTTPS reverse proxy, TCP/UDP L4 proxy, WebSocket, HTTP-to-gRPC translation
- **Service discovery** — Consul, etcd, Kubernetes, or static backends
- **Advanced load balancing** — Round-robin, least-connections, consistent-hash, least-response-time, weighted splits
- **Built-in security** — JWT/JWKS, OAuth 2.0/OIDC, mTLS, API keys, WAF (Coraza), CSRF, IP/geo filtering
- **Resilience** — Retries with budgets, hedging, circuit breakers, adaptive concurrency, outlier detection, timeouts
- **Traffic control** — Rate limiting, throttling, bandwidth limits, priority queues, canary deployments, A/B testing
- **Observability** — Prometheus metrics, OpenTelemetry tracing, structured logging (zap), event webhooks
- **Zero-downtime ops** — Hot config reload via SIGHUP or admin API, TLS certificate rotation
- **Single binary** — No runtime dependencies. Add Redis for distributed features, or run fully standalone

## Quick Start

### Install from source

```bash
# Requires Go 1.25+
git clone https://github.com/wudi/gateway.git
cd gateway
make build
```

### Run

```bash
./build/gateway -config configs/gateway.yaml
```

### Docker

```bash
# Build
docker build -t gateway .

# Run
docker run -p 8080:8080 -p 8081:8081 \
  -v $(pwd)/configs:/app/configs:ro \
  gateway
```

### Docker Compose

```bash
# Gateway + mock backends
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

## Feature Overview

### Security

| Feature | Description |
|---|---|
| **Authentication** | API key, JWT/JWKS (auto-refresh), OAuth 2.0/OIDC, mTLS |
| **External Auth** | Delegate to HTTP/gRPC auth services with response caching |
| **IP Filtering** | CIDR-based allow/deny lists (global + per-route) |
| **Geo Filtering** | MaxMind/IPDB country/city blocking with shadow mode |
| **CORS** | Regex origins, credential support, private network access |
| **WAF** | Coraza WAF with OWASP CRS (block/detect modes) |
| **CSRF** | HMAC-signed double-submit cookies with origin validation |
| **Nonce** | Replay attack prevention (in-memory or Redis) |
| **Idempotency** | Request deduplication with in-flight coalescing |

### Resilience

| Feature | Description |
|---|---|
| **Retries** | Exponential backoff with jitter and retry budgets |
| **Hedging** | Speculative parallel requests to reduce tail latency |
| **Circuit Breaker** | Three-state breaker (closed/open/half-open) via gobreaker |
| **Timeouts** | Request, backend, idle, and header timeouts with Retry-After |
| **Health Checks** | Active probing with configurable intervals per backend |
| **Adaptive Concurrency** | AIMD-based dynamic concurrency limits |
| **Outlier Detection** | Automatic ejection of unhealthy backends |

### Traffic Management

| Feature | Description |
|---|---|
| **Rate Limiting** | Fixed window or sliding window (Redis), per-IP or per-client |
| **Throttling** | Token bucket queuing with configurable burst |
| **Bandwidth Limiting** | Per-route request/response bandwidth caps |
| **Priority Queues** | Client-priority-based admission control |
| **Canary Deployments** | Progressive rollout with automated health-based rollback |
| **Traffic Splitting** | Weighted A/B testing with sticky sessions |
| **Traffic Mirroring** | Shadow traffic with conditions and response comparison |
| **API Versioning** | Path, header, accept, or query-based version routing |
| **Fault Injection** | Configurable delays and aborts for chaos testing |

### Observability

| Feature | Description |
|---|---|
| **Metrics** | Prometheus endpoint with per-route and per-feature counters |
| **Tracing** | OpenTelemetry with OTLP export (gRPC/HTTP) |
| **Logging** | Structured logging via zap with configurable access logs |
| **Webhooks** | Event notifications for backend, circuit breaker, canary, and config changes |
| **Admin Dashboard** | Aggregated health and feature stats at `/dashboard` |

### Request Processing

| Feature | Description |
|---|---|
| **Body Transform** | JSON manipulation via gjson/sjson (add, remove, rename, allow/deny, template) |
| **Header Transform** | Add, remove, or set request/response headers with variable interpolation |
| **Request Validation** | JSON Schema validation (request and response) |
| **OpenAPI Validation** | Validate against OpenAPI specs with automatic route generation |
| **GraphQL Protection** | Query depth/complexity limits, introspection control |
| **Rules Engine** | Cloudflare-style expressions for request/response rules |
| **Custom Error Pages** | Per-status-code error pages with class fallback |
| **Compression** | gzip response compression |
| **Caching** | In-memory LRU or distributed Redis cache with GraphQL-aware keys |
| **Request Coalescing** | Singleflight deduplication of identical concurrent requests |

## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │              Listeners                   │
                    │         HTTP / TCP / UDP / TLS           │
                    └────────────────┬────────────────────────┘
                                     │
                    ┌────────────────▼────────────────────────┐
                    │          Global Middleware               │
                    │  Recovery → RequestID → mTLS → Tracing  │
                    │              → Logging                   │
                    └────────────────┬────────────────────────┘
                                     │
                    ┌────────────────▼────────────────────────┐
                    │           Route Matching                 │
                    └────────────────┬────────────────────────┘
                                     │
                    ┌────────────────▼────────────────────────┐
                    │       Per-Route Middleware Chain         │
                    │                                         │
                    │  IP Filter → Geo → CORS → Rate Limit   │
                    │  → Throttle → Auth → CSRF → Rules      │
                    │  → WAF → Validation → Cache → Circuit   │
                    │  Breaker → Compression → Transform      │
                    │  → Mirror → ...                         │
                    └────────────────┬────────────────────────┘
                                     │
              ┌──────────────────────┼──────────────────────┐
              │                      │                      │
     ┌────────▼──────┐    ┌─────────▼────────┐   ┌────────▼───────┐
     │  HTTP Proxy   │    │  WebSocket Proxy  │   │  gRPC Proxy    │
     │  + Retries    │    │                   │   │  (Protocol     │
     │  + Hedging    │    │                   │   │   Translation) │
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

## Admin API

The admin API runs on a separate port (default `:8081`) and exposes operational endpoints:

```bash
# Health & readiness
curl localhost:8081/health
curl localhost:8081/ready

# Aggregated dashboard
curl localhost:8081/dashboard

# Prometheus metrics
curl localhost:8081/metrics

# Hot reload configuration
curl -X POST localhost:8081/reload

# Feature-specific status
curl localhost:8081/routes
curl localhost:8081/backends
curl localhost:8081/circuit-breakers
curl localhost:8081/rate-limits
curl localhost:8081/cache
curl localhost:8081/canary
```

See [docs/admin-api.md](docs/admin-api.md) for the full endpoint reference.

## Service Discovery

Gateway integrates with major service registries to dynamically discover backends:

```yaml
# Consul
registry:
  type: consul
  consul:
    address: "localhost:8500"
    datacenter: dc1

# etcd
registry:
  type: etcd
  etcd:
    endpoints: ["localhost:2379"]

# Kubernetes (in-cluster)
registry:
  type: kubernetes
  kubernetes:
    namespace: default
    label_selector: "app=myservice"
    in_cluster: true

# Static (with REST API for dynamic registration)
registry:
  type: memory
  memory:
    api_enabled: true
    api_port: 8082
```

## CLI

```
Usage: gateway [flags]

Flags:
  -config string    Path to configuration file (default "configs/gateway.yaml")
  -validate         Validate configuration and exit
  -version          Print version and exit
```

Signals:
- `SIGHUP` — reload configuration without downtime
- `SIGINT` / `SIGTERM` — graceful shutdown

## Documentation

Full documentation is available in the [docs/](docs/) directory:

- [Getting Started](docs/getting-started.md)
- [Core Concepts](docs/core-concepts.md)
- [Configuration Reference](docs/configuration-reference.md)
- [Admin API](docs/admin-api.md)
- [Authentication](docs/authentication.md)
- [Load Balancing](docs/load-balancing.md)
- [Resilience](docs/resilience.md) (retries, circuit breakers, hedging, timeouts)
- [Traffic Management](docs/traffic-management.md) (rate limiting, throttling, canary, A/B)
- [Security](docs/security.md) (WAF, CORS, IP/geo filtering, CSRF)
- [Caching](docs/caching.md)
- [Observability](docs/observability.md) (metrics, tracing, logging, webhooks)
- [Rules Engine](docs/rules-engine.md)
- [GraphQL](docs/graphql.md)
- [Protocol Translation](docs/protocol-translation.md) (HTTP-to-gRPC)

## Development

```bash
# Dependencies
make deps

# Build
make build

# Run tests
make test

# Run tests with coverage
make test-coverage

# Format & lint
make fmt
make lint

# Validate config
make validate

# Build for all platforms
make build-all

# Multi-arch Docker image
make docker-buildx
```

## License

MIT
