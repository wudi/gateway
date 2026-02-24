# Gateway

A high-performance API gateway and reverse proxy for HTTP, TCP, and UDP with 90+ built-in middleware features.

Gateway provides enterprise-grade traffic management, security, resilience, and observability out of the box through declarative YAML configuration. No plugins, no scripting languages, no external dependencies required — just a single binary and a config file.

## Highlights

- **Multi-protocol** — HTTP/HTTPS reverse proxy, TCP/UDP L4 proxy, WebSocket, SSE, gRPC (native + Web), HTTP-to-Thrift, HTTP/3 QUIC, HTTP CONNECT tunneling
- **Backend integrations** — Static backends, Consul/etcd/Kubernetes service discovery, AWS Lambda, AMQP/RabbitMQ, Pub/Sub (GCP, AWS, NATS, Kafka), FastCGI
- **Advanced load balancing** — Round-robin, least-connections, consistent-hash, least-response-time, weighted splits
- **Built-in security** — JWT/JWKS, OAuth 2.0/OIDC, mTLS, API keys, WAF (Coraza), CSRF, OPA, IP/geo filtering, PII redaction, field encryption, WASM plugins
- **Resilience** — Retries with budgets, hedging, circuit breakers, adaptive concurrency, outlier detection, load shedding, backpressure, SLO enforcement
- **Traffic control** — Rate limiting, spike arrest, quotas, throttling, bandwidth limits, priority queues, consumer groups, multi-tenancy, canary/blue-green/A/B
- **Observability** — Prometheus metrics, OpenTelemetry tracing, structured logging (zap), event webhooks, audit logging, developer portal
- **Zero-downtime ops** — Hot config reload via SIGHUP or admin API, graceful shutdown with connection draining, schema evolution validation
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
| **OPA Policy Engine** | Open Policy Agent integration for fine-grained authorization |
| **Token Exchange** | RFC 8693 OAuth2 STS — swap external IdP tokens for internal tokens |
| **IP Filtering** | CIDR-based allow/deny lists (global + per-route) |
| **Dynamic IP Blocklist** | Subscribe to external threat feeds for automatic IP blocking |
| **Geo Filtering** | MaxMind/IPDB country/city blocking with shadow mode |
| **CORS** | Regex origins, credential support, private network access |
| **WAF** | Coraza WAF with OWASP CRS (block/detect modes) |
| **CSRF** | HMAC-signed double-submit cookies with origin validation |
| **Nonce** | Replay attack prevention (in-memory or Redis) |
| **Idempotency** | Request deduplication with in-flight coalescing |
| **Bot Detection** | Regex-based User-Agent deny/allow lists |
| **SSRF Protection** | Block outbound connections to private/internal IP ranges |
| **Request Deduplication** | Content-hash dedup for duplicate webhook deliveries |
| **Per-Route Client mTLS** | Route-level client certificate verification with per-route CA pools |
| **Inbound Request Signing** | HMAC signature verification on incoming requests |
| **Field-Level Encryption** | AES-GCM-256 encryption/decryption of specific JSON fields |
| **PII Redaction** | Auto-detect and mask PII (emails, SSNs, credit cards) in bodies |
| **WASM Plugins** | Sandboxed custom filters in Rust/C/Go/AssemblyScript via Wazero |

### Resilience

| Feature | Description |
|---|---|
| **Retries** | Exponential backoff with jitter and retry budgets |
| **Shared Retry Budget Pools** | Cross-route shared retry budgets to prevent retry storms |
| **Hedging** | Speculative parallel requests to reduce tail latency |
| **Circuit Breaker** | Three-state breaker (closed/open/half-open) via gobreaker with admin overrides |
| **Timeouts** | Request, backend, idle, and header timeouts with Retry-After |
| **Health Checks** | Active probing with configurable intervals per backend |
| **Adaptive Concurrency** | AIMD-based dynamic concurrency limits |
| **Outlier Detection** | Automatic ejection of unhealthy backends |
| **Backend Backpressure** | Auto-remove overloaded backends via 429/503 + Retry-After detection |
| **Load Shedding** | CPU/memory/goroutine threshold-based request rejection |
| **SLI/SLO Enforcement** | Error budget tracking with log, header, or shed-load actions |
| **Schema Evolution** | Detect breaking OpenAPI spec changes on config reload |
| **Graceful Shutdown** | Configurable shutdown timeout with connection draining |

### Traffic Management

| Feature | Description |
|---|---|
| **Rate Limiting** | Fixed window or sliding window (Redis), per-IP or per-client, tiered |
| **Service Rate Limiting** | Global gateway-wide throughput cap |
| **Spike Arrest** | Continuous per-second rate enforcement with burst tolerance |
| **Quota Enforcement** | Per-client hourly/daily/monthly/yearly usage caps |
| **Consumer Groups** | Named consumer tiers with per-group rate limits, quotas, and priority |
| **Request Cost Tracking** | Assign per-operation costs, enforce per-consumer cost budgets |
| **Throttling** | Token bucket queuing with configurable burst |
| **Bandwidth Limiting** | Per-route request/response bandwidth caps |
| **Priority Queues** | Client-priority-based admission control |
| **Request Queuing** | Bounded FIFO queue absorbing traffic spikes |
| **Multi-Tenancy** | Per-tenant rate limits, quotas, timeouts, circuit breakers, cache isolation |
| **Canary Deployments** | Progressive rollout with automated health-based rollback |
| **Blue-Green Deployments** | Atomic traffic cutover with observation window and auto-rollback |
| **A/B Testing** | Weighted splits with per-group metrics (error rate, p99 latency) |
| **Traffic Mirroring** | Shadow traffic with conditions and response comparison |
| **Traffic Replay** | Record live requests and replay against a different backend |
| **API Versioning** | Path, header, accept, or query-based version routing |
| **API Deprecation** | RFC 8594 deprecation headers with sunset date blocking |
| **Fault Injection** | Configurable delays and aborts for chaos testing |

### Protocol Support

| Feature | Description |
|---|---|
| **HTTP/HTTPS Proxy** | Full reverse proxy with retries, hedging, and transforms |
| **TCP/UDP Proxy** | L4 proxying for non-HTTP protocols |
| **WebSocket** | Transparent WebSocket upgrade and proxying |
| **SSE Proxy** | Server-Sent Events with per-event flushing and heartbeat injection |
| **gRPC Proxy** | Deadline propagation, metadata transforms, message size limits, reflection |
| **gRPC-Web** | Browser-to-gRPC translation via gRPC-Web framing |
| **HTTP-to-gRPC** | REST-to-gRPC protocol translation with field mapping |
| **HTTP-to-Thrift** | REST-to-Thrift protocol translation |
| **HTTP/3 (QUIC)** | HTTP/3 over QUIC for inbound and outbound connections |
| **HTTP CONNECT** | TCP tunneling through the gateway via CONNECT method |
| **GraphQL Protection** | Query depth/complexity limits, introspection control, operation rate limits |
| **GraphQL Subscriptions** | WebSocket-based GraphQL subscriptions with connection lifecycle |
| **GraphQL Federation** | Schema stitching across multiple GraphQL backends |
| **AWS Lambda** | HTTP-to-Lambda invocation with API Gateway-style payload |
| **AMQP/RabbitMQ** | HTTP-to-AMQP message queue bridging |
| **Pub/Sub** | HTTP-to-pub/sub (GCP, AWS SNS/SQS, NATS, Kafka, Azure) via Go CDK |
| **FastCGI** | PHP-FPM and FastCGI backend proxying |

### Caching & Performance

| Feature | Description |
|---|---|
| **Response Caching** | In-memory LRU or distributed Redis cache with GraphQL-aware keys |
| **Stale-While-Revalidate** | Serve stale cache entries while revalidating in the background |
| **Shared Cache Buckets** | Cross-route cache store sharing via named buckets |
| **CDN Cache Headers** | Inject Cache-Control, Vary, and Surrogate-Control headers |
| **ETag Generation** | SHA-256 ETags with If-None-Match / 304 support |
| **Request Coalescing** | Singleflight deduplication of identical concurrent requests |
| **Response Streaming** | Configurable flush behavior (immediate or periodic) for streaming APIs |
| **Compression** | gzip, deflate, brotli, zstd response compression |
| **Request Decompression** | Decompress inbound gzip/deflate/br/zstd with zip bomb protection |

### Request & Response Processing

| Feature | Description |
|---|---|
| **Header Transform** | Add, remove, or set request/response headers with variable interpolation |
| **Body Transform** | JSON manipulation via gjson/sjson (add, remove, rename, allow/deny, template) |
| **Data Manipulation** | JMESPath query language for JSON response filtering and reshaping |
| **Response Flatmap** | Array manipulation and data extraction (KrakenD-style) |
| **Content Negotiation** | Accept-based JSON/XML/YAML output conversion |
| **Content Replacer** | Regex-based string replacement in response bodies and headers |
| **Body Generator** | Go template-based request body generation (GET-to-POST translation) |
| **Response Body Generator** | Go template-based full response rewriting |
| **Backend Encoding** | Auto-decode XML/YAML backend responses to JSON |
| **Status Code Mapping** | Remap backend status codes to client-facing codes |
| **Response Signing** | HMAC-sign outbound responses for downstream tamper verification |
| **Response Size Limiting** | Maximum response body size per route or globally |
| **Parameter Forwarding** | Zero-trust allow-listing of headers, query params, and cookies |
| **URL Rewriting** | Prefix stripping, regex rewrite, host override |
| **Follow Redirects** | Transparent backend 3xx redirect following |
| **Request Validation** | JSON Schema validation (request and response) |
| **OpenAPI Validation** | Validate against OpenAPI specs with automatic route generation |
| **Rules Engine** | Cloudflare-style expressions for request/response rules |
| **Custom Error Pages** | Per-status-code error pages with class fallback |
| **Static File Serving** | Serve static files directly without a backend |
| **Mock Responses** | Static or OpenAPI-generated mock responses |
| **Raw Body Passthrough** | Skip all body-processing middleware for binary protocols |
| **Baggage Propagation** | Inject contextual key-value pairs into upstream request headers |

### Routing & Backend Patterns

| Feature | Description |
|---|---|
| **Path-Based Routing** | Exact and prefix path matching with variable extraction |
| **Load Balancing** | Round-robin, least-conn, consistent-hash, least-response-time, weighted |
| **Service Discovery** | Consul, etcd, Kubernetes, or static backends with live watch |
| **Sequential Proxy** | Multi-step backend chaining with data piping between steps |
| **Response Aggregation** | Parallel multi-backend fan-out with JSON merge |

### Observability

| Feature | Description |
|---|---|
| **Metrics** | Prometheus endpoint with per-route and per-feature counters |
| **Tracing** | OpenTelemetry with OTLP export (gRPC/HTTP) |
| **Logging** | Structured logging via zap with configurable access logs |
| **Webhooks** | Event notifications for backend, circuit breaker, canary, and config changes |
| **Audit Logging** | Async webhook delivery of request/response records for compliance |
| **Admin Dashboard** | Aggregated health and feature stats at `/dashboard` |
| **Debug Endpoint** | Runtime request inspection and configuration summary |
| **Developer Portal** | Browsable API catalog with OpenAPI spec viewer (Redoc) |
| **gRPC Health Server** | Native `grpc.health.v1.Health` for Kubernetes gRPC probes |

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
     │  + Retries    │    │     Proxy         │   │  (Protocol     │
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

### Getting Started
- [Getting Started](docs/getting-started.md) — Installation, CLI flags, minimal config
- [Core Concepts](docs/core-concepts.md) — Listeners, routes, backends, request pipeline
- [Configuration Reference](docs/configuration-reference.md) — Complete YAML schema
- [Examples](docs/examples.md) — Full worked configurations
- [Benchmarks](docs/benchmarks.md) — Performance benchmarks and tuning

### Traffic & Routing
- [Load Balancing](docs/load-balancing.md) — Round-robin, least-conn, consistent-hash, least-response-time
- [Traffic Management](docs/traffic-management.md) — Traffic splits, A/B testing, sticky sessions
- [Canary Deployments](docs/canary-deployments.md) — Progressive rollouts with metrics-based promotion
- [Blue-Green Deployments](docs/blue-green.md) — Atomic cutover with observation window
- [A/B Testing](docs/ab-testing.md) — Per-group metrics on traffic splits
- [API Versioning](docs/api-versioning.md) — Path, header, accept, query-based version routing
- [API Deprecation](docs/deprecation.md) — RFC 8594 deprecation headers and sunset blocking
- [URL Rewriting](docs/url-rewriting.md) — Prefix stripping, regex rewrite, host override
- [Service Discovery](docs/service-discovery.md) — Consul, etcd, Kubernetes, memory registry
- [Sequential Proxy](docs/sequential-proxy.md) — Multi-step backend chaining with data piping
- [Response Aggregation](docs/response-aggregation.md) — Parallel multi-backend calls with JSON merge
- [Follow Redirects](docs/follow-redirects.md) — Backend redirect following
- [Passthrough](docs/passthrough.md) — Raw body passthrough mode

### Protocol Support
- [Protocol Translation](docs/protocol-translation.md) — HTTP-to-gRPC, HTTP-to-Thrift, WebSocket proxy
- [GraphQL Protection](docs/graphql.md) — Depth/complexity limits, introspection, operation rate limits
- [GraphQL Federation](docs/graphql-federation.md) — Schema stitching across multiple GraphQL backends
- [GraphQL Subscriptions](docs/graphql-subscriptions.md) — WebSocket-based subscriptions
- [gRPC Proxy](docs/grpc-proxy.md) — Deadline propagation, metadata transforms, reflection
- [gRPC-Web](docs/grpc-web.md) — Browser-to-gRPC translation
- [gRPC Health](docs/grpc-health.md) — Native `grpc.health.v1` for Kubernetes probes
- [HTTP/3 & QUIC](docs/http3.md) — HTTP/3 listener and upstream support
- [HTTP CONNECT](docs/http-connect.md) — TCP tunneling via CONNECT method
- [SSE Proxy](docs/sse-proxy.md) — Server-Sent Events with heartbeat and event injection
- [AMQP/RabbitMQ](docs/amqp.md) — HTTP-to-AMQP message queue bridging
- [Pub/Sub](docs/pubsub.md) — HTTP-to-pub/sub (GCP, AWS, NATS, Kafka, Azure)
- [AWS Lambda](docs/lambda.md) — HTTP-to-Lambda invocation
- [FastCGI](docs/fastcgi.md) — PHP-FPM and FastCGI backend proxying

### Resilience
- [Resilience](docs/resilience.md) — Retries, budget, hedging, circuit breakers, timeouts
- [Circuit Breaker Controls](docs/circuit-breaker-controls.md) — Runtime admin overrides
- [Shared Retry Budget Pools](docs/retry-budget-pools.md) — Cross-route shared retry budgets
- [Adaptive Concurrency](docs/adaptive-concurrency.md) — AIMD-based concurrency control
- [Backend Backpressure](docs/backpressure.md) — Auto-detect overloaded backends
- [Load Shedding](docs/load-shedding.md) — CPU/memory/goroutine threshold-based rejection
- [SLI/SLO Enforcement](docs/slo.md) — Error budget tracking with configurable actions
- [Schema Evolution](docs/schema-evolution.md) — Breaking change detection on config reload
- [Graceful Shutdown](docs/graceful-shutdown.md) — Shutdown timeout, connection draining
- [Transport](docs/transport.md) — HTTP transport pool configuration

### Rate Limiting & Traffic Shaping
- [Rate Limiting & Throttling](docs/rate-limiting-and-throttling.md) — Rate limits, throttle, bandwidth, priority, fault injection
- [Service Rate Limiting](docs/service-rate-limiting.md) — Global service-level throughput cap
- [Spike Arrest](docs/spike-arrest.md) — Per-second burst protection
- [Quota](docs/quota.md) — Daily/hourly/monthly/yearly quota enforcement
- [Consumer Groups](docs/consumer-groups.md) — Named consumer tiers with per-group policies
- [Request Cost Tracking](docs/request-cost.md) — Cost-based metering and budget enforcement
- [Multi-Tenancy](docs/multi-tenancy.md) — Per-tenant resource isolation
- [Request Queuing](docs/request-queuing.md) — Bounded FIFO queue for traffic spikes

### Security
- [Security](docs/security.md) — IP filtering, CORS, WAF, body limits, trusted proxies
- [Authentication](docs/authentication.md) — API key, JWT/JWKS, OAuth/OIDC, mTLS
- [External Auth](docs/external-auth.md) — Delegated auth via HTTP/gRPC service
- [OPA Policy Engine](docs/opa.md) — Open Policy Agent authorization
- [Token Exchange](docs/token-exchange.md) — RFC 8693 OAuth2 STS
- [CSRF Protection](docs/csrf.md) — Double-submit cookies with origin validation
- [Idempotency](docs/idempotency.md) — Idempotency key support for safe retries
- [Replay Prevention](docs/replay-prevention.md) — Nonce-based replay attack prevention
- [Bot Detection](docs/bot-detection.md) — User-Agent regex deny/allow lists
- [SSRF Protection](docs/ssrf-protection.md) — Block connections to private IPs
- [Request Deduplication](docs/request-dedup.md) — Content-hash dedup for webhooks
- [Dynamic IP Blocklist](docs/ip-blocklist.md) — External threat feed subscriptions
- [Client mTLS](docs/client-mtls.md) — Per-route client certificate verification
- [Inbound Signing](docs/inbound-signing.md) — HMAC request signature verification
- [Field Encryption](docs/field-encryption.md) — AES-GCM-256 per-field JSON encryption
- [PII Redaction](docs/pii-redaction.md) — Auto-mask PII in request/response bodies
- [WASM Plugins](docs/wasm-plugins.md) — Sandboxed custom filters via Wazero

### Caching
- [Caching](docs/caching.md) — Response caching, GraphQL-aware cache keys
- [Shared Cache Buckets](docs/shared-cache-buckets.md) — Cross-route cache sharing
- [CDN Cache Headers](docs/cdn-cache-headers.md) — Cache-Control and Surrogate-Control injection
- [ETag](docs/etag.md) — SHA-256 ETag generation with conditional request support
- [Response Streaming](docs/response-streaming.md) — Flush control for streaming APIs

### Transformations
- [Transformations](docs/transformations.md) — Headers, body, variables, path rewrite, compression
- [Data Manipulation](docs/data-manipulation.md) — JMESPath query language for response filtering
- [Response Body Generator](docs/response-body-generator.md) — Go template-based response rewriting
- [Response Flatmap](docs/response-flatmap.md) — Array manipulation and data extraction
- [Content Negotiation](docs/content-negotiation.md) — Accept-based JSON/XML/YAML conversion
- [Content Replacer](docs/content-replacer.md) — Regex replacement in response bodies
- [Parameter Forwarding](docs/parameter-forwarding.md) — Zero-trust header/query/cookie forwarding
- [Body Generator](docs/body-generator.md) — Request body generation from templates
- [Backend Encoding](docs/backend-encoding.md) — Backend response re-encoding
- [Status Mapping](docs/status-mapping.md) — Response status code remapping
- [Response Signing](docs/response-signing.md) — HMAC-sign outbound responses
- [Response Limits](docs/response-limits.md) — Response size limiting
- [Validation](docs/validation.md) — Request/response JSON schema and OpenAPI validation
- [Baggage Propagation](docs/baggage-propagation.md) — Inject context into upstream headers
- [Static Files](docs/static-files.md) — Static file serving
- [Mock Responses](docs/mock-responses.md) — Static mock response configuration
- [Error Pages](docs/error-pages.md) — Custom error page templates

### Observability
- [Observability](docs/observability.md) — Logging, Prometheus metrics, OpenTelemetry tracing
- [Webhooks](docs/webhooks.md) — Event notification via HTTP webhooks
- [Audit Logging](docs/audit-logging.md) — Request/response records for compliance
- [Debug Endpoint](docs/debug-endpoint.md) — Runtime debug information
- [Traffic Mirroring](docs/traffic-mirroring.md) — Shadow traffic and response comparison
- [Traffic Replay](docs/traffic-replay.md) — Record and replay live traffic
- [Developer Portal](docs/developer-portal.md) — Browsable API catalog with OpenAPI viewer

### Reference
- [Admin API Reference](docs/admin-api.md) — Health, feature endpoints, dashboard, reload
- [Rules Engine](docs/rules-engine.md) — Expression syntax, request/response rules, actions

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
