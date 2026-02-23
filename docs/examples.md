# Examples

Full worked configurations for common deployment scenarios.

## Minimal HTTP Proxy

The simplest possible configuration — proxy all requests to a single backend:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

routes:
  - id: "app"
    path: "/"
    path_prefix: true
    backends:
      - url: "http://localhost:9000"
```

## HTTPS with TLS Termination

Terminate TLS at the gateway and proxy to plain HTTP backends:

```yaml
listeners:
  - id: "https"
    address: ":443"
    protocol: "http"
    tls:
      enabled: true
      cert_file: "/etc/certs/server.crt"
      key_file: "/etc/certs/server.key"
    http:
      read_timeout: 30s
      write_timeout: 30s
      idle_timeout: 120s

routes:
  - id: "app"
    path: "/"
    path_prefix: true
    backends:
      - url: "http://app-server:9000"
    compression:
      enabled: true
      content_types: ["application/json", "text/html"]
```

## Multi-Listener (HTTP + HTTPS + TCP + UDP)

Run HTTP, HTTPS, TCP, and UDP listeners simultaneously:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

  - id: "https"
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

routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://api-server:9000"

tcp_routes:
  - id: "postgres"
    listener: "tcp-db"
    match:
      sni: ["db.example.com"]
    backends:
      - url: "tcp://postgres:5432"

udp_routes:
  - id: "dns"
    listener: "udp-dns"
    backends:
      - url: "udp://8.8.8.8:53"
      - url: "udp://8.8.4.4:53"
```

## JWT-Authenticated API with Rate Limiting

Protect an API with JWT auth, rate limiting, and retry policy:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

authentication:
  jwt:
    enabled: true
    jwks_url: "https://auth.example.com/.well-known/jwks.json"
    jwks_refresh_interval: 1h
    issuer: "https://auth.example.com"
    audience: ["my-api"]

routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    backends:
      - url: "http://api-1:9000"
      - url: "http://api-2:9000"
    auth:
      required: true
      methods: ["jwt"]
    rate_limit:
      enabled: true
      rate: 100
      period: 1m
      burst: 20
      per_ip: true
    retry_policy:
      max_retries: 2
      initial_backoff: 100ms
      max_backoff: 1s
      backoff_multiplier: 2.0
      retryable_statuses: [502, 503, 504]
      retryable_methods: ["GET"]
    circuit_breaker:
      enabled: true
      failure_threshold: 5
      timeout: 30s
      max_requests: 2
    transform:
      request:
        headers:
          add:
            X-Request-ID: "$request_id"
            X-Forwarded-For: "$remote_addr"
      response:
        headers:
          add:
            X-Request-ID: "$request_id"
```

## A/B Testing with Traffic Splits

Route 90% of traffic to stable, 10% to canary with sticky sessions:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

routes:
  - id: "app"
    path: "/"
    path_prefix: true
    traffic_split:
      - name: "stable"
        weight: 90
        backends:
          - url: "http://v1-app:9000"
      - name: "canary"
        weight: 10
        backends:
          - url: "http://v2-app:9000"
    sticky:
      enabled: true
      mode: "cookie"
      cookie_name: "X-Traffic-Group"
      ttl: 24h
```

## gRPC Translation with REST Mappings

Expose a gRPC service as a REST API:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

routes:
  - id: "user-api"
    path: "/users"
    path_prefix: true
    backends:
      - url: "http://grpc-server:50051"
    protocol:
      type: "http_to_grpc"
      grpc:
        service: "myapp.UserService"
        timeout: 10s
        mappings:
          - http_method: "GET"
            http_path: "/users/:user_id"
            grpc_method: "GetUser"
            body: ""
          - http_method: "POST"
            http_path: "/users"
            grpc_method: "CreateUser"
            body: "*"
          - http_method: "PUT"
            http_path: "/users/:user_id"
            grpc_method: "UpdateUser"
            body: "*"
          - http_method: "DELETE"
            http_path: "/users/:user_id"
            grpc_method: "DeleteUser"
            body: ""
```

## Thrift Translation with REST Mappings

Expose a Thrift service as a REST API:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

routes:
  - id: "user-thrift"
    path: "/users"
    path_prefix: true
    backends:
      - url: "http://thrift-server:9090"
    protocol:
      type: "http_to_thrift"
      thrift:
        idl_file: "/etc/idl/user_service.thrift"
        service: "UserService"
        timeout: 10s
        protocol: "binary"
        transport: "framed"
        mappings:
          - http_method: "GET"
            http_path: "/users/:user_id"
            thrift_method: "GetUser"
            body: ""
          - http_method: "POST"
            http_path: "/users"
            thrift_method: "CreateUser"
            body: "*"
          - http_method: "PUT"
            http_path: "/users/:user_id"
            thrift_method: "UpdateUser"
            body: "*"
          - http_method: "DELETE"
            http_path: "/users/:user_id"
            thrift_method: "DeleteUser"
            body: ""
```

## GraphQL API with Protection

Front a GraphQL backend with depth, complexity, and introspection controls:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

routes:
  - id: "graphql"
    path: "/graphql"
    backends:
      - url: "http://graphql-server:4000"
    graphql:
      enabled: true
      max_depth: 10
      max_complexity: 200
      introspection: false
      operation_limits:
        query: 100
        mutation: 20
    cache:
      enabled: true
      ttl: 1m
      max_size: 1000
      methods: ["GET", "POST"]
    rate_limit:
      enabled: true
      rate: 500
      period: 1m
      per_ip: true
```

## GraphQL Federation

Merge two GraphQL backends into a single unified endpoint:

```yaml
listeners:
  - id: "http"
    address: ":8080"
    protocol: "http"

routes:
  - id: "unified-graphql"
    path: "/graphql"
    graphql_federation:
      enabled: true
      refresh_interval: 5m
      sources:
        - name: users
          url: "http://users-service:4000/graphql"
        - name: orders
          url: "http://orders-service:4001/graphql"
    rate_limit:
      enabled: true
      rate: 200
      period: 1m
      per_ip: true
```

Clients query `/graphql` and the gateway splits cross-backend queries, fans out to the owning sources, and merges the responses. See [GraphQL Federation](graphql-federation.md) for details.

## Production-Ready with Observability

Full production configuration with logging, metrics, tracing, WAF, and health checks:

```yaml
listeners:
  - id: "https"
    address: ":443"
    protocol: "http"
    tls:
      enabled: true
      cert_file: "/etc/certs/server.crt"
      key_file: "/etc/certs/server.key"
    http:
      read_timeout: 30s
      write_timeout: 60s
      idle_timeout: 120s

registry:
  type: "consul"
  consul:
    address: "consul.service.consul:8500"
    datacenter: "dc1"
    token: "${CONSUL_TOKEN}"

authentication:
  jwt:
    enabled: true
    jwks_url: "https://auth.example.com/.well-known/jwks.json"
    issuer: "https://auth.example.com"
    audience: ["production-api"]
  api_key:
    enabled: true
    header: "X-API-Key"

redis:
  address: "redis.service.consul:6379"
  password: "${REDIS_PASSWORD}"
  pool_size: 20

ip_filter:
  enabled: true
  deny:
    - "192.0.2.0/24"

waf:
  enabled: true
  mode: "block"
  sql_injection: true
  xss: true

rules:
  response:
    - id: "security-headers"
      expression: "true"
      action: "set_headers"
      headers:
        set:
          X-Content-Type-Options: "nosniff"
          X-Frame-Options: "DENY"
          Strict-Transport-Security: "max-age=31536000; includeSubDomains"

traffic_shaping:
  priority:
    enabled: true
    max_concurrent: 500
    max_wait: 30s
    default_level: 5

routes:
  - id: "api"
    path: "/api"
    path_prefix: true
    service:
      name: "api-service"
      tags: ["production"]
    auth:
      required: true
      methods: ["jwt", "api_key"]
    rate_limit:
      enabled: true
      rate: 1000
      period: 1m
      burst: 50
      mode: "distributed"
    retry_policy:
      max_retries: 2
      initial_backoff: 50ms
      max_backoff: 500ms
      backoff_multiplier: 2.0
      retryable_statuses: [502, 503, 504]
      retryable_methods: ["GET"]
      budget:
        ratio: 0.1
        min_retries: 3
    circuit_breaker:
      enabled: true
      failure_threshold: 10
      timeout: 60s
      max_requests: 3
    cache:
      enabled: true
      ttl: 30s
      max_size: 5000
      methods: ["GET"]
    cors:
      enabled: true
      allow_origins: ["https://app.example.com"]
      allow_methods: ["GET", "POST", "PUT", "DELETE"]
      allow_headers: ["Authorization", "Content-Type"]
      allow_credentials: true
      max_age: 3600
    compression:
      enabled: true
      content_types: ["application/json", "text/html"]
    transform:
      request:
        headers:
          add:
            X-Request-ID: "$request_id"
            X-Forwarded-For: "$remote_addr"
            X-Forwarded-Proto: "$scheme"
      response:
        headers:
          add:
            X-Request-ID: "$request_id"
    mirror:
      enabled: true
      percentage: 5
      backends:
        - url: "http://shadow-api:9000"
      compare:
        enabled: true
        log_mismatches: true

logging:
  level: "info"
  output: "stdout"
  format: '$remote_addr - [$time_iso8601] "$request_method $request_uri" $status $body_bytes_sent "$http_user_agent" $response_time'

admin:
  enabled: true
  port: 8081
  metrics:
    enabled: true
  readiness:
    min_healthy_backends: 2
    require_redis: true

tracing:
  enabled: true
  exporter: "otlp"
  endpoint: "otel-collector:4317"
  service_name: "api-gateway"
  sample_rate: 0.1
  insecure: true

dns_resolver:
  nameservers:
    - "10.0.0.53:53"
  timeout: 5s
```
