# API Gateway User Manual

A high-performance, feature-rich API gateway supporting HTTP, TCP, and UDP proxying with extensive middleware capabilities.

## Table of Contents

### Getting Started

- [Getting Started](getting-started.md) — Installation, CLI flags, minimal config
- [Core Concepts](core-concepts.md) — Listeners, routes, backends, request pipeline
- [Examples](examples.md) — Full worked configurations
- [Benchmarks](benchmarks.md) — Performance benchmarks and tuning

### Traffic & Routing

- [Load Balancing](load-balancing.md) — Round-robin, least-conn, consistent-hash, least-response-time
- [Traffic Management](traffic-management.md) — Traffic splits, A/B testing, sticky sessions
- [Canary Deployments](canary-deployments.md) — Progressive rollouts with metrics-based promotion
- [API Versioning](api-versioning.md) — Path, header, accept, query-based version routing
- [URL Rewriting](url-rewriting.md) — Prefix stripping, regex rewrite, host override
- [Service Discovery](service-discovery.md) — Consul, etcd, Kubernetes, memory registry
- [Sequential Proxy](sequential-proxy.md) — Multi-step sequential backend calls with data piping
- [Response Aggregation](response-aggregation.md) — Parallel multi-backend calls with JSON merge
- [Follow Redirects](follow-redirects.md) — Backend redirect following
- [Passthrough](passthrough.md) — Raw body passthrough mode

### Protocol

- [Protocol Translation](protocol-translation.md) — HTTP-to-gRPC, HTTP-to-Thrift, REST mappings, WebSocket proxy
- [GraphQL Protection](graphql.md) — Depth/complexity limits, introspection, operation rate limits
- [GraphQL Federation](graphql-federation.md) — Schema stitching across multiple GraphQL backends
- [gRPC Proxy](grpc-proxy.md) — gRPC-aware proxying with deadline propagation, metadata transforms, reflection
- [HTTP/3 & QUIC](http3.md) — HTTP/3 listener and upstream support
- [SSE Proxy](sse-proxy.md) — Server-Sent Events proxy with heartbeat, event injection, and streaming

### Resilience

- [Resilience](resilience.md) — Retries, budget, hedging, circuit breakers, timeouts
- [Adaptive Concurrency](adaptive-concurrency.md) — AIMD-based concurrency control
- [Graceful Shutdown](graceful-shutdown.md) — Shutdown timeout, connection draining
- [Transport](transport.md) — HTTP transport pool configuration

### Rate Limiting & Traffic Shaping

- [Rate Limiting & Throttling](rate-limiting-and-throttling.md) — Rate limits, throttle, bandwidth, priority, fault injection
- [Service Rate Limiting](service-rate-limiting.md) — Global service-level throughput cap
- [Spike Arrest](spike-arrest.md) — Per-second burst protection
- [Quota](quota.md) — Daily/hourly quota enforcement

### Security

- [Security](security.md) — IP filtering, CORS, WAF, body limits, DNS resolver
- [Authentication](authentication.md) — API key, JWT/JWKS, OAuth/OIDC, mTLS
- [External Auth](external-auth.md) — Delegated auth via HTTP/gRPC service
- [CSRF Protection](csrf.md) — Cross-site request forgery prevention
- [Idempotency](idempotency.md) — Idempotency key support for safe retries
- [Replay Prevention](replay-prevention.md) — Nonce-based replay attack prevention
- [Bot Detection](bot-detection.md) — User-Agent regex deny/allow lists
- [SSRF Protection](ssrf-protection.md) — Block outbound connections to private IPs
- [Request Deduplication](request-dedup.md) — Content-hash dedup for duplicate webhook deliveries
- [Dynamic IP Blocklist](ip-blocklist.md) — Subscribe to external threat feeds for auto-blocking

### Caching

- [Caching](caching.md) — Response caching, GraphQL-aware cache keys
- [Shared Cache Buckets](shared-cache-buckets.md) — Cross-route cache sharing
- [CDN Cache Headers](cdn-cache-headers.md) — Cache-Control and Surrogate-Control injection

### Transformations

- [Transformations](transformations.md) — Headers, body, variables, path rewrite, validation, compression
- [Response Body Generator](response-body-generator.md) — Go template-based response rewriting
- [Response Flatmap](response-flatmap.md) — Array manipulation and data extraction
- [Content Negotiation](content-negotiation.md) — Accept-based JSON/XML/YAML conversion
- [Content Replacer](content-replacer.md) — String/regex replacement in response bodies
- [Parameter Forwarding](parameter-forwarding.md) — Zero-trust header/query/cookie forwarding
- [Body Generator](body-generator.md) — Request body generation from templates
- [Backend Encoding](backend-encoding.md) — Backend response re-encoding
- [Status Mapping](status-mapping.md) — Response status code remapping
- [Response Limits](response-limits.md) — Response size limiting
- [Validation](validation.md) — Request/response JSON schema validation
- [Static Files](static-files.md) — Static file serving
- [FastCGI Proxy](fastcgi.md) — PHP-FPM and FastCGI backend proxying
- [Mock Responses](mock-responses.md) — Static mock response configuration
- [Error Pages](error-pages.md) — Custom error page templates

### Observability

- [Observability](observability.md) — Logging, Prometheus metrics, OpenTelemetry tracing
- [Webhooks](webhooks.md) — Event notification via HTTP webhooks
- [Debug Endpoint](debug-endpoint.md) — Runtime debug information
- [Traffic Mirroring](traffic-mirroring.md) — Shadow traffic, conditions, comparison

### Reference

- [Developer Portal](developer-portal.md) — Browsable API catalog with OpenAPI spec viewer
- [Admin API Reference](admin-api.md) — Health, feature endpoints, dashboard, reload
- [Configuration Reference](configuration-reference.md) — Complete YAML schema
- [Rules Engine](rules-engine.md) — Expression syntax, request/response rules, actions
