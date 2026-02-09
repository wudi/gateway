# Configuration Reference

Complete YAML configuration schema. All fields, types, defaults, and validation constraints.

Values support environment variable expansion via `${VAR}` syntax. Durations use Go syntax: `30s`, `5m`, `1h`.

---

## Listeners

```yaml
listeners:
  - id: string              # required, unique identifier
    address: string          # required, bind address (e.g., ":8080")
    protocol: string         # required: "http", "tcp", or "udp"
    tls:
      enabled: bool          # enable TLS (default false)
      cert_file: string      # path to TLS certificate
      key_file: string       # path to TLS private key
      ca_file: string        # path to CA certificate
      client_auth: string    # mTLS mode: "none", "request", "require", "verify"
      client_ca_file: string # path to client CA for mTLS
    http:
      read_timeout: duration       # default 30s
      write_timeout: duration      # default 30s
      idle_timeout: duration       # default 60s
      max_header_bytes: int        # max header size (bytes)
      read_header_timeout: duration
    tcp:
      sni_routing: bool         # enable SNI-based routing
      connect_timeout: duration
      idle_timeout: duration
      proxy_protocol: bool      # enable PROXY protocol
    udp:
      session_timeout: duration
      read_buffer_size: int
      write_buffer_size: int
```

**Validation:** At least one listener required. If TLS enabled, both `cert_file` and `key_file` required.

---

## Registry

```yaml
registry:
  type: string     # required: "consul", "etcd", "kubernetes", or "memory"
  consul:
    address: string      # default "localhost:8500"
    scheme: string       # "http" or "https"
    datacenter: string   # default "dc1"
    token: string        # API token
    namespace: string
  etcd:
    endpoints: [string]  # etcd server addresses
    username: string
    password: string
    tls:                 # same structure as listener TLS
      enabled: bool
      cert_file: string
      key_file: string
      ca_file: string
  kubernetes:
    namespace: string       # default "default"
    label_selector: string  # e.g., "app=myservice"
    in_cluster: bool        # use in-cluster auth
    kube_config: string     # path to kubeconfig
  memory:
    api_enabled: bool    # enable REST API for registration
    api_port: int        # default 8082
```

---

## Authentication

```yaml
authentication:
  api_key:
    enabled: bool
    header: string         # default "X-API-Key"
    query_param: string    # alternative: check query parameter
    keys:
      - key: string        # the API key value
        client_id: string  # client identifier
        name: string       # human-readable name
        expires_at: string # RFC3339 expiration (optional)
        roles: [string]    # role list (optional)
  jwt:
    enabled: bool
    secret: string              # HMAC secret (HS256)
    public_key: string          # RSA public key (RS256)
    algorithm: string           # default "HS256"
    issuer: string              # expected issuer claim
    audience: [string]          # expected audience claim(s)
    jwks_url: string            # JWKS endpoint for dynamic keys
    jwks_refresh_interval: duration  # default 1h
  oauth:
    enabled: bool
    introspection_url: string   # token introspection endpoint
    client_id: string
    client_secret: string
    jwks_url: string
    jwks_refresh_interval: duration
    issuer: string
    audience: string
    scopes: [string]            # required OAuth scopes
    cache_ttl: duration         # token cache TTL
```

**Validation:** JWT requires at least one of `secret`, `public_key`, or `jwks_url`.

---

## Routes

```yaml
routes:
  - id: string               # required, unique identifier
    path: string              # required, URL path
    path_prefix: bool         # prefix match (default false = exact)
    methods: [string]         # HTTP methods (empty = all)
    match:
      domains: [string]
      headers:
        - name: string        # required
          value: string       # exact match (mutually exclusive)
          present: bool       # presence check (mutually exclusive)
          regex: string       # regex match (mutually exclusive)
      query:
        - name: string
          value: string
          present: bool
          regex: string
    backends:
      - url: string           # required, backend URL
        weight: int           # load balancer weight (0-100)
    service:
      name: string            # service discovery name
      tags: [string]          # service tags filter
    auth:
      required: bool
      methods: [string]       # "jwt", "api_key", "oauth"
    timeout: duration
    retries: int              # simple retry count (use retry_policy for advanced)
    strip_prefix: bool
    max_body_size: int64      # max request body (bytes)
    load_balancer: string     # "round_robin", "least_conn", "consistent_hash", "least_response_time"
    consistent_hash:
      key: string             # "header", "cookie", "path", "ip"
      header_name: string     # required for header/cookie
      replicas: int           # virtual nodes (default 150)
```

**Validation:** Each route requires `path` and either `backends` or `service.name`. Header/query matchers require exactly one of `value`, `present`, or `regex`.

### Rate Limiting

```yaml
    rate_limit:
      enabled: bool
      rate: int               # requests per period
      period: duration
      burst: int              # token bucket burst
      per_ip: bool            # per-IP or per-route
      mode: string            # "local" (default) or "distributed"
      algorithm: string       # "token_bucket" (default) or "sliding_window"
```

**Validation:** Distributed mode requires top-level `redis.address`. Algorithm `"sliding_window"` is incompatible with mode `"distributed"` (distributed already uses a sliding window via Redis).

### Retry Policy

```yaml
    retry_policy:
      max_retries: int
      initial_backoff: duration
      max_backoff: duration
      backoff_multiplier: float    # must be >= 1.0
      retryable_statuses: [int]    # 100-599
      retryable_methods: [string]
      per_try_timeout: duration
      budget:
        ratio: float               # 0.0-1.0
        min_retries: int           # >= 0
        window: duration           # > 0 (default 10s)
      hedging:
        enabled: bool
        max_requests: int          # >= 2 (default 2)
        delay: duration
```

**Validation:** `max_retries > 0` and `hedging.enabled` are mutually exclusive.

### Timeout Policy

```yaml
    timeout_policy:
      request: duration
      idle: duration
```

### Circuit Breaker

```yaml
    circuit_breaker:
      enabled: bool
      failure_threshold: int    # > 0
      max_requests: int         # > 0 (half-open limit)
      timeout: duration         # > 0 (open → half-open)
```

### Cache

```yaml
    cache:
      enabled: bool
      ttl: duration             # > 0
      max_size: int             # > 0 (max entries)
      max_body_size: int64      # max response body to cache
      methods: [string]         # e.g., ["GET"]
      key_headers: [string]     # extra headers in cache key
```

### Coalesce (Request Coalescing)

```yaml
    coalesce:
      enabled: bool
      timeout: duration         # max wait for coalesced requests (default 30s, >= 0)
      key_headers: [string]     # headers included in coalesce key
      methods: [string]         # eligible methods (default ["GET", "HEAD"])
```

**Validation:** `timeout` must be >= 0. `methods` must be valid HTTP methods.

### Canary Deployments

```yaml
    canary:
      enabled: bool
      canary_group: string        # must match a traffic_split group name
      steps:
        - weight: int             # 0-100, monotonically non-decreasing
          pause: duration         # hold duration before next step
      analysis:
        error_threshold: float    # 0.0-1.0 (rollback threshold)
        latency_threshold: duration  # max p99 before rollback
        min_requests: int         # min samples before evaluation
        interval: duration        # evaluation frequency (default 30s)
```

**Validation:** Requires `traffic_split`. `canary_group` must exist in traffic splits. At least one step required. Step weights must be 0-100 and monotonically non-decreasing. `error_threshold` must be 0.0-1.0.

See [Canary Deployments](canary-deployments.md) for full documentation.

### WebSocket

```yaml
    websocket:
      enabled: bool
      read_buffer_size: int      # > 0 if set
      write_buffer_size: int     # > 0 if set
      read_timeout: duration
      write_timeout: duration
      ping_interval: duration
      pong_timeout: duration
```

**Validation:** If `read_buffer_size` or `write_buffer_size` is set, it must be > 0.

### CORS

```yaml
    cors:
      enabled: bool
      allow_origins: [string]
      allow_origin_patterns: [string]    # regex patterns
      allow_methods: [string]
      allow_headers: [string]
      expose_headers: [string]
      allow_credentials: bool
      allow_private_network: bool
      max_age: int                       # seconds
```

**Validation:** `allow_origin_patterns` must be valid regexes.

### Compression

```yaml
    compression:
      enabled: bool
      level: int              # 1-9 (default 6)
      min_size: int           # min bytes to compress (default 1024)
      content_types: [string] # MIME types to compress
```

### Transforms

```yaml
    transform:
      request:
        headers:
          add: {string: string}
          set: {string: string}
          remove: [string]
        body:
          add_fields: {string: string}       # top-level field add
          remove_fields: [string]             # dot-path field removal
          rename_fields: {string: string}     # field rename (old: new)
          set_fields: {string: string}        # dot-path field set with $var support
          allow_fields: [string]              # allowlist filter (mutually exclusive with deny)
          deny_fields: [string]               # denylist filter (mutually exclusive with allow)
          template: string                    # Go text/template for full body reshaping
      response:
        headers:
          add: {string: string}
          set: {string: string}
          remove: [string]
        body:
          add_fields: {string: string}
          remove_fields: [string]
          rename_fields: {string: string}
          set_fields: {string: string}
          allow_fields: [string]
          deny_fields: [string]
          template: string
```

**Validation:** `allow_fields` and `deny_fields` are mutually exclusive. `template` must be a valid Go `text/template`.

### Validation

```yaml
    validation:
      enabled: bool
      schema: string               # inline JSON schema
      schema_file: string          # path to JSON schema file
      response_schema: string      # inline JSON schema for response validation
      response_schema_file: string # path to response JSON schema file
      log_only: bool               # log validation errors instead of rejecting (default false)
```

**Validation:** `schema` and `schema_file` are mutually exclusive. `response_schema` and `response_schema_file` are mutually exclusive. Uses `santhosh-tekuri/jsonschema/v6` for full JSON Schema support (draft 4/6/7/2019-09/2020-12) including `minLength`, `pattern`, `enum`, `$ref`, `oneOf`/`anyOf`/`allOf`.

### OpenAPI Validation (per-route)

```yaml
    openapi:
      spec_file: string        # path to OpenAPI 3.x spec file
      spec_id: string          # reference to top-level spec by ID (mutually exclusive with spec_file)
      operation_id: string     # specific operation to validate against
      validate_request: bool   # validate requests (default true)
      validate_response: bool  # validate responses (default false)
      log_only: bool           # log errors instead of rejecting (default false)
```

**Validation:** `spec_file` and `spec_id` are mutually exclusive. When `spec_id` is used, the ID must reference a spec defined in the top-level `openapi.specs` section.

### Traffic Split

```yaml
    traffic_split:
      - name: string
        weight: int                    # 0-100
        backends:
          - url: string
            weight: int
        match_headers: {string: string}
```

**Validation:** All weights must sum to 100.

### Sticky Sessions

```yaml
    sticky:
      enabled: bool
      mode: string            # "cookie", "header", "hash"
      cookie_name: string     # default "X-Traffic-Group"
      hash_key: string        # required for header/hash modes
      ttl: duration           # default 24h
```

**Validation:** Requires `traffic_split` to be configured. `hash_key` required for header/hash modes.

### IP Filter (per-route)

```yaml
    ip_filter:
      enabled: bool
      allow: [string]         # CIDR blocks
      deny: [string]
      order: string           # "allow_first" (default) or "deny_first"
```

### Mirror

```yaml
    mirror:
      enabled: bool
      percentage: int          # 0-100
      backends:
        - url: string
          weight: int
      conditions:
        methods: [string]
        headers: {string: string}
        path_regex: string     # must be valid regex
      compare:
        enabled: bool
        log_mismatches: bool
```

### Rules (per-route)

```yaml
    rules:
      request:
        - id: string           # required, unique
          enabled: bool        # default true
          expression: string   # required, expr-lang expression
          action: string       # block, custom_response, redirect, set_headers, rewrite, group, log
          status_code: int     # for block/custom_response (100-599)
          body: string         # for custom_response
          redirect_url: string # for redirect
          headers:             # for set_headers
            add: {string: string}
            set: {string: string}
            remove: [string]
          rewrite:             # for rewrite
            path: string
            query: string
            headers: {add, set, remove}
          group: string        # for group action
          log_message: string  # for log action
          description: string
      response:               # same structure, limited actions (set_headers only)
```

### Protocol Translation

```yaml
    protocol:
      type: string            # "http_to_grpc"
      grpc:
        service: string       # fully-qualified gRPC service name
        method: string        # fixed method (requires service)
        timeout: duration     # default 30s
        descriptor_cache_ttl: duration  # default 5m
        tls:
          enabled: bool
          cert_file: string
          key_file: string
          ca_file: string
        mappings:
          - http_method: string  # GET, POST, PUT, DELETE, PATCH
            http_path: string    # /path/:param or /path/{param}
            grpc_method: string
            body: string         # "", "*", or "field_name"
```

**Validation:** Mutually exclusive with `grpc.enabled`. `method` and `mappings` are mutually exclusive. If `grpc.tls.enabled` is true, `ca_file` is required. If `mappings` is used, `service` is required. `method` requires `service`.

### gRPC Passthrough

```yaml
    grpc:
      enabled: bool
```

### Traffic Shaping (per-route)

```yaml
    traffic_shaping:
      throttle:
        enabled: bool
        rate: int           # tokens/sec (> 0)
        burst: int          # >= 0
        max_wait: duration
        per_ip: bool
      bandwidth:
        enabled: bool
        request_rate: int64   # bytes/sec (0 = unlimited)
        response_rate: int64
        request_burst: int64  # defaults to request_rate
        response_burst: int64
      priority:
        enabled: bool
        levels:
          - level: int            # 1-10
            headers: {string: string}
            client_ids: [string]
      fault_injection:
        enabled: bool
        delay:
          percentage: int       # 0-100
          duration: duration    # > 0 if percentage > 0
        abort:
          percentage: int       # 0-100
          status_code: int      # 100-599
      adaptive_concurrency:
        enabled: bool
        min_concurrency: int      # default 5
        max_concurrency: int      # default 1000
        latency_tolerance: float  # default 2.0, must be >= 1.0
        adjustment_interval: duration  # default 5s
        smoothing_factor: float   # default 0.5, 0 < x < 1
        min_latency_samples: int  # default 25
```

### WAF (per-route)

```yaml
    waf:
      enabled: bool
      mode: string          # "block" or "detect"
      rule_files: [string]
      inline_rules: [string]
      sql_injection: bool
      xss: bool
```

### GraphQL

```yaml
    graphql:
      enabled: bool
      max_depth: int          # >= 0 (0 = unlimited)
      max_complexity: int     # >= 0 (0 = unlimited)
      introspection: bool     # default false
      operation_limits:       # per-type rate limits
        query: int            # > 0
        mutation: int
        subscription: int
```

### External Auth

```yaml
    ext_auth:
      enabled: bool
      url: string              # http://, https://, or grpc:// URL
      timeout: duration        # default 5s
      fail_open: bool          # default false (fail closed)
      headers_to_send: [string]   # request headers to forward (empty = all)
      headers_to_inject: [string] # response headers to inject upstream (empty = all)
      cache_ttl: duration      # 0 = no caching
      tls:
        enabled: bool
        ca_file: string
        cert_file: string      # for mTLS to auth service
        key_file: string
```

**Validation:** `url` is required when enabled and must start with `http://`, `https://`, or `grpc://`. `timeout` and `cache_ttl` must be >= 0. TLS cannot be used with `http://` URLs.

---

### `versioning`

```yaml
    versioning:
      enabled: bool
      source: string           # "path", "header", "accept", "query"
      header_name: string      # default "X-API-Version" (for header source)
      query_param: string      # default "version" (for query source)
      path_prefix: string      # default "/v" (for path source)
      strip_prefix: bool       # strip /vN from forwarded path (path source only)
      default_version: string  # required, must exist in versions
      versions:
        "<version>":
          backends:
            - url: string
              weight: int
          deprecated: bool     # adds Deprecation: true header
          sunset: string       # adds Sunset header (YYYY-MM-DD format)
```

**Validation:** `source` must be one of: path, header, accept, query. `versions` must not be empty. `default_version` is required and must exist in versions. Each version must have at least one backend. Mutually exclusive with `traffic_split` and top-level `backends`. `sunset` must be YYYY-MM-DD if set.

---

### Access Log

```yaml
    access_log:
      enabled: bool              # nil=inherit global, false=disable route logging
      format: string             # override global log format
      headers_include: [string]  # headers to log (mutually exclusive with headers_exclude)
      headers_exclude: [string]  # headers to exclude from logging
      sensitive_headers: [string] # additional headers to mask (merged with defaults)
      body:
        enabled: bool            # default false
        max_size: int            # max bytes to capture (default 4096)
        content_types: [string]  # MIME types to capture (empty = all)
        request: bool            # capture request body
        response: bool           # capture response body
      conditions:
        status_codes: [string]   # "4xx", "5xx", "200", "200-299"
        methods: [string]        # "POST", "DELETE"
        sample_rate: float       # 0.0-1.0 (0 = log all)
```

**Defaults:** Sensitive headers always masked: `Authorization`, `Cookie`, `Set-Cookie`, `X-API-Key`. Body `max_size` defaults to 4096.

**Validation:** `headers_include` and `headers_exclude` are mutually exclusive. `sample_rate` must be 0.0-1.0. `status_codes` must be valid patterns. `methods` must be valid HTTP methods. `body.max_size` must be >= 0 when body enabled.

---

## TCP Routes

```yaml
tcp_routes:
  - id: string             # required
    listener: string        # required, references listener ID
    match:
      sni: [string]         # SNI patterns
      source_cidr: [string] # CIDR blocks
    backends:
      - url: string         # tcp://host:port
        weight: int
```

---

## UDP Routes

```yaml
udp_routes:
  - id: string
    listener: string
    backends:
      - url: string         # udp://host:port
        weight: int
```

---

## Security

### IP Filter (global)

```yaml
ip_filter:
  enabled: bool             # enable IP filtering
  allow: [string]           # CIDR blocks
  deny: [string]
  order: string             # "allow_first" (default) or "deny_first"
```

### WAF (global)

```yaml
waf:
  enabled: bool
  mode: string              # "block" or "detect"
  rule_files: [string]
  inline_rules: [string]
  sql_injection: bool
  xss: bool
```

### DNS Resolver

```yaml
dns_resolver:
  nameservers: [string]     # host:port format (e.g., "10.0.0.53:53")
  timeout: duration         # per-query timeout (> 0)
```

---

## Rules (global)

```yaml
rules:
  request: [RuleConfig]     # same structure as per-route rules
  response: [RuleConfig]
```

---

## Traffic Shaping (global)

```yaml
traffic_shaping:
  throttle:
    enabled: bool
    rate: int
    burst: int
    max_wait: duration
    per_ip: bool
  bandwidth:
    enabled: bool
    request_rate: int64
    response_rate: int64
    request_burst: int64
    response_burst: int64
  priority:
    enabled: bool
    max_concurrent: int     # > 0, shared semaphore capacity
    max_wait: duration
    default_level: int      # 1-10 (default 5)
    levels: [PriorityLevelConfig]
  fault_injection:
    enabled: bool
    delay:
      percentage: int
      duration: duration
    abort:
      percentage: int
      status_code: int
  adaptive_concurrency:
    enabled: bool
    min_concurrency: int      # default 5
    max_concurrency: int      # default 1000
    latency_tolerance: float  # default 2.0, must be >= 1.0
    adjustment_interval: duration  # default 5s
    smoothing_factor: float   # default 0.5, 0 < x < 1
    min_latency_samples: int  # default 25
```

---

## OpenAPI

```yaml
openapi:
  specs:
    - id: string                # required, unique identifier for spec
      file: string              # required, path to OpenAPI 3.x spec file
      default_backends:         # required, backends for generated routes
        - url: string
          weight: int
      route_prefix: string      # prefix prepended to all paths (e.g., "/api")
      strip_prefix: bool        # strip route_prefix before proxying
      validation:
        request: bool           # validate requests (default true)
        response: bool          # validate responses (default false)
        log_only: bool          # log errors instead of rejecting (default false)
```

**Validation:** Each spec requires `id` (unique), `file`, and non-empty `default_backends`. Routes are auto-generated at config load time from each spec's paths and operations.

---

## Observability

### Logging

```yaml
logging:
  level: string             # "debug", "info", "warn", "error" (default "info")
  output: string            # "stdout", "stderr", or file path (default "stdout")
  format: string            # access log format with $variable substitution
```

### Tracing

```yaml
tracing:
  enabled: bool
  exporter: string          # "otlp"
  endpoint: string          # OTLP collector address
  service_name: string
  sample_rate: float        # 0.0-1.0
  insecure: bool            # insecure gRPC connection
  headers: {string: string} # extra OTLP headers
```

---

## Admin

```yaml
admin:
  enabled: bool             # default true
  port: int                 # default 8081
  metrics:
    enabled: bool
    path: string            # default "/metrics"
  readiness:
    min_healthy_backends: int  # default 1
    require_redis: bool
```

---

## Redis

```yaml
redis:
  address: string           # host:port
  password: string
  db: int                   # database number
  tls: bool
  pool_size: int
  dial_timeout: duration
```

Required for distributed rate limiting (`rate_limit.mode: "distributed"`).
