---
title: "Core Concepts"
sidebar_position: 2
---

## Listeners

Listeners define network endpoints where the runway accepts connections. Each listener has a unique ID, bind address, and protocol.

The runway supports three protocols:
- **HTTP** — standard HTTP/HTTPS reverse proxy with the full middleware chain
- **TCP** — Layer 4 TCP proxy with optional SNI-based routing and PROXY protocol
- **UDP** — Layer 4 UDP proxy with session tracking

Multiple listeners can run simultaneously on different ports and protocols.

```yaml
listeners:
  - id: "http-main"
    address: ":8080"
    protocol: "http"
    http:
      read_timeout: 30s
      write_timeout: 30s
      idle_timeout: 60s

  - id: "https-main"
    address: ":8443"
    protocol: "http"
    tls:
      enabled: true
      cert_file: "/etc/certs/server.crt"
      key_file: "/etc/certs/server.key"

  - id: "tcp-db"
    address: ":5432"
    protocol: "tcp"
    tcp:
      sni_routing: true
      connect_timeout: 10s
      idle_timeout: 5m

  - id: "udp-dns"
    address: ":5353"
    protocol: "udp"
    udp:
      session_timeout: 30s
```

### TLS Termination

Any HTTP listener can terminate TLS by setting `tls.enabled: true` with certificate and key paths. TLS certificates can be hot-reloaded on config reload (SIGHUP) without dropping connections.

### mTLS (Mutual TLS)

For client certificate authentication, set `tls.client_auth` and `tls.client_ca_file`:

```yaml
tls:
  enabled: true
  cert_file: "/etc/certs/server.crt"
  key_file: "/etc/certs/server.key"
  client_auth: "require"    # none, request, require, verify
  client_ca_file: "/etc/certs/client-ca.crt"
```

Client certificate fields become available as [variables](../transformations/transformations.md#variables) (`$client_cert_subject`, etc.) and in the [rules engine](../reference/rules-engine.md).

## Routes

Routes map incoming requests to backend services. HTTP routes match on path, method, domain, headers, and query parameters.

```yaml
routes:
  - id: "users-api"
    path: "/api/v1/users"
    path_prefix: true          # match /api/v1/users and all sub-paths
    methods: ["GET", "POST"]   # restrict to specific methods
    match:
      domains: ["api.example.com"]
      headers:
        - name: "X-Version"
          value: "v2"
      query:
        - name: "format"
          value: "json"
    backends:
      - url: "http://users-svc:9000"
```

### Path Matching

- **Exact match** (`path_prefix: false`): `/health` matches only `/health`
- **Prefix match** (`path_prefix: true`): `/api` matches `/api`, `/api/users`, `/api/users/123`, etc.

### Domain, Header, Query, and Cookie Matching

The `match` block adds additional constraints beyond path:

- **Domains**: request `Host` header must match one of the listed domains
- **Headers**: each entry requires `name` plus exactly one of `value` (exact), `present` (exists), or `regex` (pattern)
- **Query**: same as headers but for query parameters
- **Cookies**: same as headers but for request cookies

### Body Field Matching

Routes can match on JSON request body fields using [gjson](https://github.com/tidwall/gjson) path syntax. Body matching requires `Content-Type: application/json`; non-JSON requests skip body matchers.

The body is read once per route group and restored for downstream handlers. A configurable `max_match_body_size` (default 1MB) caps how much of the body is read — oversized bodies cause body matchers to evaluate as false without error.

```yaml
routes:
  - id: "create-user"
    path: /api/actions
    match:
      body:
        - name: "action"
          value: "create"
        - name: "entity.type"
          value: "user"
    backends:
      - url: "http://create-svc:9000"

  - id: "delete-action"
    path: /api/actions
    match:
      body:
        - name: "action"
          value: "delete"
      max_match_body_size: 524288  # 512KB
    backends:
      - url: "http://delete-svc:9000"
```

Supported match modes per field (mutually exclusive):
- **`value`**: exact string match on the field value
- **`present`**: `true` requires the field to exist, `false` requires it to be absent
- **`regex`**: regex match on the field's string value

The `name` field uses gjson path syntax for nested access: `"user.role"`, `"items.#"`, `"meta.tags.0"`.

### TCP and UDP Routes

L4 routes reference a listener by ID and define their own backends:

```yaml
tcp_routes:
  - id: "mysql"
    listener: "tcp-db"
    match:
      sni: ["mysql.example.com"]
      source_cidr: ["10.0.0.0/8"]
    backends:
      - url: "tcp://mysql-primary:3306"

udp_routes:
  - id: "dns"
    listener: "udp-dns"
    backends:
      - url: "udp://8.8.8.8:53"
```

## Backends

Each route sends traffic to one or more backends. Backends can be defined inline on the route, resolved through [service discovery](../traffic-routing/service-discovery.md), or referenced from a named upstream.

```yaml
# Static backends with weights (inline)
backends:
  - url: "http://backend-1:9000"
    weight: 2
  - url: "http://backend-2:9000"
    weight: 1

# Or via service discovery
service:
  name: "users-service"
  tags: ["production"]

# Or via a named upstream (see below)
upstream: "my-api-pool"
```

Backend health is checked automatically. Unhealthy backends are removed from rotation until they recover. See [Load Balancing](../traffic-routing/load-balancing.md) for algorithm options.

## Upstreams

Upstreams are named backend pools that can be shared across multiple routes. When several routes use the same set of backends, define them once as an upstream instead of duplicating the backend list.

```yaml
upstreams:
  api-pool:
    backends:
      - url: "http://api-1:9000"
      - url: "http://api-2:9000"
    load_balancer: "least_conn"
    health_check:
      path: "/healthz"
      interval: 15s

routes:
  - id: "users"
    path: "/users"
    upstream: "api-pool"

  - id: "orders"
    path: "/orders"
    upstream: "api-pool"
```

An upstream can define `backends` (static) or `service` (discovery), a load balancer algorithm, consistent hash config, and health check overrides. Routes reference upstreams by name via the `upstream` field. A route cannot have both `upstream` and `backends` (or `service`).

The `upstream` field is also supported on traffic split groups, versioning version entries, and mirror config, allowing shared pools in those contexts too.

Health check config merges in three levels: global defaults → upstream-level overrides → per-backend overrides.

## Echo Routes

An echo route has no backend. The runway itself acts as the origin and returns the incoming request details as JSON. This is useful for debugging, testing middleware chains, and verifying runway behavior without spinning up a separate service.

```yaml
routes:
  - id: "debug"
    path: "/debug"
    echo: true
```

The response includes the HTTP method, path, host, remote address, query parameters, headers, request body, a timestamp, and the route ID. The request body is capped at 1 MB.

Echo routes still pass through the full middleware chain, so features like rate limiting, authentication, CORS, IP filtering, geo filtering, rules, WAF, compression, timeouts, and access logging all work normally. Features that require a real backend (circuit breaker, cache, websocket, mirroring, retries, traffic splits, canary, outlier detection, versioning) are not allowed and will fail config validation.

## Request Processing Pipeline

Every HTTP request passes through the middleware chain in a fixed order. The chain is built once per route at startup (not per-request). Here is the processing order:

1. **Metrics** — start timing
2. **IP Filter** — reject blocked IPs (403)
3. **CORS** — handle preflight, set response headers
4. **Variable Context** — populate route ID and path params
5. **Rate Limit** — reject over-limit requests (429)
6. **Throttle** — queue/delay requests via token bucket (503 on timeout)
7. **Authentication** — validate credentials
8. **Priority** — admission control with QoS levels (503 on timeout)
9. **Request Rules** — evaluate expression-based rules (block/redirect/rewrite)
10. **WAF** — web application firewall inspection
11. **Fault Injection** — inject delays/aborts for chaos testing
12. **Body Limit** — enforce max request body size
13. **Bandwidth** — rate-limit request/response I/O
14. **Validation** — validate request body against JSON schema
15. **GraphQL** — parse and enforce query limits
16. **WebSocket** — upgrade if applicable (bypasses remaining chain)
17. **Cache** — return cached response if hit
18. **Circuit Breaker** — reject if circuit is open (503)
19. **Compression** — gzip response
20. **Response Rules** — evaluate post-proxy rules
21. **Mirror** — send shadow traffic asynchronously
22. **Traffic Group** — set A/B variant headers and cookies
23. **Request Transform** — modify headers/body before proxy
24. **Response Body Transform** — modify response JSON
25. **Proxy** — forward to backend (with retry/hedging)

## Global Handler Chain

Before the per-route middleware chain, every request passes through global handlers:

1. **Recovery** — panic recovery, returns 500
2. **Request ID** — generate/propagate `X-Request-ID`
3. **mTLS Extraction** — extract client certificate info
4. **Tracing** — OpenTelemetry span creation
5. **Logging** — structured access logging
6. **serveHTTP** — route matching and dispatch to per-route handler
