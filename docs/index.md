---
title: "API Gateway User Manual"
sidebar_position: 0
slug: /
---

A high-performance, feature-rich API gateway supporting HTTP, TCP, and UDP proxying with extensive middleware capabilities.

## Table of Contents

### Getting Started

- [Getting Started](getting-started/getting-started.md) — Installation, CLI flags, minimal config
- [Core Concepts](getting-started/core-concepts.md) — Listeners, routes, backends, request pipeline
- [Examples](getting-started/examples.md) — Full worked configurations
- [Benchmarks](getting-started/benchmarks.md) — Performance benchmarks and tuning

### Traffic & Routing

- [Load Balancing](traffic-routing/load-balancing.md) — Round-robin, least-conn, consistent-hash, least-response-time
- [Traffic Management](traffic-routing/traffic-management.md) — Traffic splits, A/B testing, sticky sessions
- [Canary Deployments](traffic-routing/canary-deployments.md) — Progressive rollouts with metrics-based promotion
- [API Versioning](traffic-routing/api-versioning.md) — Path, header, accept, query-based version routing
- [URL Rewriting](traffic-routing/url-rewriting.md) — Prefix stripping, regex rewrite, host override
- [Service Discovery](traffic-routing/service-discovery.md) — Consul, etcd, Kubernetes, memory registry
- [Sequential Proxy](traffic-routing/sequential-proxy.md) — Multi-step sequential backend calls with data piping
- [Response Aggregation](traffic-routing/response-aggregation.md) — Parallel multi-backend calls with JSON merge
- [Follow Redirects](traffic-routing/follow-redirects.md) — Backend redirect following
- [Passthrough](traffic-routing/passthrough.md) — Raw body passthrough mode

### Protocol

- [Protocol Translation](protocol/protocol-translation.md) — HTTP-to-gRPC, HTTP-to-Thrift, REST mappings, WebSocket proxy
- [GraphQL Protection](protocol/graphql.md) — Depth/complexity limits, introspection, operation rate limits
- [GraphQL Federation](protocol/graphql-federation.md) — Schema stitching across multiple GraphQL backends
- [gRPC Proxy](protocol/grpc-proxy.md) — gRPC-aware proxying with deadline propagation, metadata transforms, reflection
- [HTTP/3 & QUIC](protocol/http3.md) — HTTP/3 listener and upstream support
- [SSE Proxy](protocol/sse-proxy.md) — Server-Sent Events proxy with heartbeat, event injection, and streaming

### Resilience

- [Resilience](resilience/resilience.md) — Retries, budget, hedging, circuit breakers, timeouts
- [Adaptive Concurrency](resilience/adaptive-concurrency.md) — AIMD-based concurrency control
- [Graceful Shutdown](resilience/graceful-shutdown.md) — Shutdown timeout, connection draining
- [Transport](resilience/transport.md) — HTTP transport pool configuration

### Rate Limiting & Traffic Shaping

- [Rate Limiting & Throttling](rate-limiting/rate-limiting-and-throttling.md) — Rate limits, throttle, bandwidth, priority, fault injection
- [Service Rate Limiting](rate-limiting/service-rate-limiting.md) — Global service-level throughput cap
- [Spike Arrest](rate-limiting/spike-arrest.md) — Per-second burst protection
- [Quota](rate-limiting/quota.md) — Daily/hourly quota enforcement

### Security

- [Security](security/security.md) — IP filtering, CORS, WAF, body limits, DNS resolver
- [Authentication](security/authentication.md) — API key, JWT/JWKS, OAuth/OIDC, mTLS
- [External Auth](security/external-auth.md) — Delegated auth via HTTP/gRPC service
- [CSRF Protection](security/csrf.md) — Cross-site request forgery prevention
- [Idempotency](security/idempotency.md) — Idempotency key support for safe retries
- [Replay Prevention](security/replay-prevention.md) — Nonce-based replay attack prevention
- [Bot Detection](security/bot-detection.md) — User-Agent regex deny/allow lists
- [SSRF Protection](security/ssrf-protection.md) — Block outbound connections to private IPs
- [Request Deduplication](security/request-dedup.md) — Content-hash dedup for duplicate webhook deliveries
- [Dynamic IP Blocklist](security/ip-blocklist.md) — Subscribe to external threat feeds for auto-blocking

### Caching

- [Caching](caching/caching.md) — Response caching, GraphQL-aware cache keys
- [Shared Cache Buckets](caching/shared-cache-buckets.md) — Cross-route cache sharing
- [CDN Cache Headers](caching/cdn-cache-headers.md) — Cache-Control and Surrogate-Control injection

### Transformations

- [Transformations](transformations/transformations.md) — Headers, body, variables, path rewrite, validation, compression
- [Response Body Generator](transformations/response-body-generator.md) — Go template-based response rewriting
- [Response Flatmap](transformations/response-flatmap.md) — Array manipulation and data extraction
- [Content Negotiation](transformations/content-negotiation.md) — Accept-based JSON/XML/YAML conversion
- [Content Replacer](transformations/content-replacer.md) — String/regex replacement in response bodies
- [Parameter Forwarding](transformations/parameter-forwarding.md) — Zero-trust header/query/cookie forwarding
- [Body Generator](transformations/body-generator.md) — Request body generation from templates
- [Backend Encoding](transformations/backend-encoding.md) — Backend response re-encoding
- [Status Mapping](transformations/status-mapping.md) — Response status code remapping
- [Response Limits](transformations/response-limits.md) — Response size limiting
- [Validation](transformations/validation.md) — Request/response JSON schema validation
- [Static Files](transformations/static-files.md) — Static file serving
- [FastCGI Proxy](protocol/fastcgi.md) — PHP-FPM and FastCGI backend proxying
- [Mock Responses](transformations/mock-responses.md) — Static mock response configuration
- [Error Pages](transformations/error-pages.md) — Custom error page templates

### Observability

- [Observability](observability/observability.md) — Logging, Prometheus metrics, OpenTelemetry tracing
- [Webhooks](observability/webhooks.md) — Event notification via HTTP webhooks
- [Debug Endpoint](observability/debug-endpoint.md) — Runtime debug information
- [Traffic Mirroring](observability/traffic-mirroring.md) — Shadow traffic, conditions, comparison

### Reference

- [Developer Portal](observability/developer-portal.md) — Browsable API catalog with OpenAPI spec viewer
- [Admin API Reference](reference/admin-api.md) — Health, feature endpoints, dashboard, reload
- [Configuration Reference](reference/configuration-reference.md) — Complete YAML schema
- [Rules Engine](reference/rules-engine.md) — Expression syntax, request/response rules, actions
- [Template Functions](reference/template-functions.md) — Sprig and custom template function reference
