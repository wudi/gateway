# API Gateway User Manual

A high-performance, feature-rich API gateway supporting HTTP, TCP, and UDP proxying with extensive middleware capabilities.

## Table of Contents

1. [Getting Started](getting-started.md) — Installation, CLI flags, minimal config
2. [Core Concepts](core-concepts.md) — Listeners, routes, backends, request pipeline
3. [Service Discovery](service-discovery.md) — Consul, etcd, Kubernetes, memory registry
4. [Authentication](authentication.md) — API key, JWT/JWKS, OAuth/OIDC, mTLS
5. [Load Balancing](load-balancing.md) — Round-robin, least-conn, consistent-hash, least-response-time
6. [Traffic Management](traffic-management.md) — Traffic splits, A/B testing, sticky sessions
7. [Resilience](resilience.md) — Retries, budget, hedging, circuit breakers, timeouts
8. [Rate Limiting & Traffic Shaping](rate-limiting-and-throttling.md) — Rate limits, throttle, bandwidth, priority, fault injection
9. [Caching](caching.md) — Response caching, GraphQL-aware cache keys
10. [Security](security.md) — IP filtering, CORS, WAF, body limits, DNS resolver
11. [Transformations](transformations.md) — Headers, body, variables, path rewrite, validation, compression
12. [Rules Engine](rules-engine.md) — Expression syntax, request/response rules, actions
13. [Protocol Translation](protocol-translation.md) — HTTP-to-gRPC, REST mappings, WebSocket proxy
14. [GraphQL Protection](graphql.md) — Depth/complexity limits, introspection, operation rate limits
15. [Observability](observability.md) — Logging, Prometheus metrics, OpenTelemetry tracing
16. [Traffic Mirroring](traffic-mirroring.md) — Shadow traffic, conditions, comparison
17. [Admin API Reference](admin-api.md) — Health, feature endpoints, dashboard, reload
18. [Configuration Reference](configuration-reference.md) — Complete YAML schema
19. [Examples](examples.md) — Full worked configurations
