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
      enable_http3: bool           # serve HTTP/3 over QUIC on same port (requires TLS)
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

**Validation:** At least one listener required. If TLS enabled, both `cert_file` and `key_file` required. `enable_http3` requires `tls.enabled`.

---

## Registry

```yaml
registry:
  type: string     # required: "consul", "etcd", "kubernetes", "memory", or "dns"
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
  dns:
    domain: string          # required: base domain (e.g., "service.consul")
    protocol: string        # default "tcp"
    nameserver: string      # optional custom DNS server "host:port"
    poll_interval: duration # default 30s
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

## Upstreams

Named backend pools that can be shared across multiple routes. Instead of duplicating backend lists on each route, define them once as an upstream and reference by name.

```yaml
upstreams:
  my-api-pool:
    backends:
      - url: "http://api-1:9000"
        weight: 2
      - url: "http://api-2:9000"
        weight: 1
    load_balancer: string     # "round_robin", "least_conn", "consistent_hash", "least_response_time"
    consistent_hash:
      key: string             # "header", "cookie", "path", "ip"
      header_name: string
      replicas: int
    health_check:             # upstream-level health check (overrides global, overridden by per-backend)
      path: string
      method: string
      interval: duration
      timeout: duration
      healthy_after: int
      unhealthy_after: int
      expected_status: [string]
    transport:                # per-upstream transport overrides (see Transport section)
      max_idle_conns: int
      max_idle_conns_per_host: int
      max_conns_per_host: int
      idle_conn_timeout: duration
      dial_timeout: duration
      tls_handshake_timeout: duration
      response_header_timeout: duration
      expect_continue_timeout: duration
      disable_keep_alives: bool
      insecure_skip_verify: bool
      ca_file: string
      cert_file: string
      key_file: string
      force_http2: bool
      enable_http3: bool   # connect via HTTP/3 over QUIC (mutually exclusive with force_http2)

  my-service-pool:
    service:
      name: "users-service"   # service discovery name
      tags: ["production"]
```

**Validation:** Each upstream must have either `backends` or `service.name`, not both. If `load_balancer` is `consistent_hash`, `consistent_hash.key` is required.

Routes reference upstreams with the `upstream` field:

```yaml
routes:
  - id: "users"
    path: "/users"
    upstream: "my-api-pool"    # references the named upstream above
```

A route cannot have both `upstream` and `backends` (or `service`). The `upstream` field is also supported on `traffic_split` groups, `versioning.versions` entries, and `mirror` config.

Health check config merges in three levels: global defaults → upstream-level overrides → per-backend overrides.

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
      cookies:
        - name: string
          value: string
          present: bool
          regex: string
      body:
        - name: string        # gjson path (required)
          value: string       # exact match (mutually exclusive)
          present: bool       # presence check (mutually exclusive)
          regex: string       # regex match (mutually exclusive)
      max_match_body_size: int64  # max body bytes for matching (default: 1048576)
    backends:
      - url: string           # required, backend URL
        weight: int           # load balancer weight (0-100)
        health_check:         # per-backend override (nil = inherit global)
          path: string
          method: string
          interval: duration
          timeout: duration
          healthy_after: int
          unhealthy_after: int
          expected_status: [string]
    service:
      name: string            # service discovery name
      tags: [string]          # service tags filter
    upstream: string           # named upstream reference (alternative to backends/service)
    auth:
      required: bool
      methods: [string]       # "jwt", "api_key", "oauth"
    timeout: duration
    retries: int              # simple retry count (use retry_policy for advanced)
    strip_prefix: bool
    rewrite:
      url: string             # full URL override (scheme://host:port/path?query) — takes precedence over prefix/regex
      prefix: string          # replace matched path prefix (requires path_prefix, mutually exclusive with strip_prefix and regex)
      regex: string           # regex pattern on request path (mutually exclusive with prefix)
      replacement: string     # regex substitution (supports $1, $2 capture groups; required with regex)
      host: string            # override Host header sent to backend (independent of path rewrite)
    max_body_size: int64      # max request body (bytes)
    load_balancer: string     # "round_robin", "least_conn", "consistent_hash", "least_response_time"
    consistent_hash:
      key: string             # "header", "cookie", "path", "ip"
      header_name: string     # required for header/cookie
      replicas: int           # virtual nodes (default 150)
    echo: bool                # built-in echo handler, no backend needed (default false)
```

**Validation:** Each route requires `path` and one of `backends`, `service.name`, `upstream`, `echo: true`, or `static.enabled: true`. A route cannot have both `upstream` and `backends` (or `service`). When `echo: true`, the route cannot use `backends`, `service`, `upstream`, `versioning`, `protocol`, `websocket`, `circuit_breaker`, `cache`, `coalesce`, `outlier_detection`, `canary`, `retry_policy`, `traffic_split`, or `mirror`. Header/query matchers require exactly one of `value`, `present`, or `regex`.

### Rate Limiting

```yaml
    rate_limit:
      enabled: bool
      rate: int               # requests per period
      period: duration
      burst: int              # token bucket burst
      per_ip: bool            # per-IP or per-route
      key: string             # custom key: "ip", "client_id", "header:<name>", "cookie:<name>", "jwt_claim:<name>"
      mode: string            # "local" (default) or "distributed"
      algorithm: string       # "token_bucket" (default) or "sliding_window"
```

**Validation:** Distributed mode requires top-level `redis.address`. Algorithm `"sliding_window"` is incompatible with mode `"distributed"` (distributed already uses a sliding window via Redis). `key` and `per_ip` are mutually exclusive. `key` must match a supported prefix (`ip`, `client_id`, `header:<name>`, `cookie:<name>`, `jwt_claim:<name>`). Falls back to client IP when the extracted value is absent.

#### Tiered Rate Limits

```yaml
    rate_limit:
      tier_key: string          # "header:<name>" or "jwt_claim:<name>"
      default_tier: string      # must exist in tiers map
      key: string               # per-client key within each tier
      tiers:
        <name>:
          rate: int
          period: duration
          burst: int
```

**Validation:** `tiers` and top-level `rate`/`period` are mutually exclusive. `default_tier` must exist in `tiers`. `tier_key` is required and must use `header:` or `jwt_claim:` prefix. Each tier requires `rate > 0`.

See [Rate Limiting & Throttling](rate-limiting-and-throttling.md#tiered-rate-limits) for examples.

### Proxy Rate Limit

```yaml
    proxy_rate_limit:
      enabled: bool
      rate: int               # max requests per period to backend
      period: duration        # default: 1s
      burst: int              # token bucket burst (default: rate)
```

**Validation:** `rate` must be > 0 when enabled.

See [Rate Limiting & Throttling](rate-limiting-and-throttling.md#proxy-rate-limiting-backend-protection) for details.

### Mock Response

```yaml
    mock_response:
      enabled: bool
      status_code: int              # default: 200
      headers: map[string]string
      body: string
```

**Validation:** `status_code` must be 100-599. Cannot combine with `echo: true`.

See [Mock Responses](mock-responses.md) for use cases.

### Bot Detection (per-route)

```yaml
    bot_detection:
      enabled: bool
      deny: [string]          # regex patterns to block
      allow: [string]         # regex patterns to allow (bypass deny)
```

**Validation:** At least one `deny` pattern required when enabled. All patterns must be valid regexes.

### Client mTLS (per-route)

```yaml
    client_mtls:
      enabled: bool
      client_auth: string         # "request"|"require"|"verify" (default "verify")
      client_ca_file: string      # path to CA PEM file
      client_cas: [string]        # paths to multiple CA PEM files
      allow_expired: bool         # skip expiry check (testing only)
```

**Validation:** `client_auth` must be `"request"`, `"require"`, or `"verify"`. Verify mode requires `client_ca_file` or `client_cas`. All CA files must exist.

### Backend Auth (OAuth2 Client Credentials)

```yaml
    backend_auth:
      enabled: bool
      type: string              # "oauth2_client_credentials" (required)
      token_url: string         # token endpoint URL (required)
      client_id: string         # OAuth2 client ID (required)
      client_secret: string     # OAuth2 client secret (required)
      scopes: [string]          # optional scopes
      extra_params:             # optional extra form parameters
        audience: string
      timeout: duration         # token fetch timeout (default 10s)
```

**Validation:** `type` must be `"oauth2_client_credentials"`. `token_url`, `client_id`, and `client_secret` are required when enabled. Tokens are cached and auto-refreshed 10 seconds before expiry.

See [Authentication](authentication.md#backend-auth-oauth2-client-credentials) for details.

### Status Mapping

```yaml
    status_mapping:
      enabled: bool
      mappings:                 # backend_code -> client_code
        404: 200
        500: 503
```

**Validation:** All mapping keys and values must be valid HTTP status codes (100-599).

### Static File Serving

```yaml
    static:
      enabled: bool
      root: string              # directory path (required)
      index: string             # index file name (default "index.html")
      browse: bool              # enable directory listing (default false)
      cache_control: string     # Cache-Control header value
```

**Validation:** `root` is required and must exist. Mutually exclusive with `echo`, `backends`, `service`, and `upstream`. When enabled, the static file handler replaces the proxy as the innermost handler.

### FastCGI Proxy

```yaml
    fastcgi:
      enabled: bool
      address: string             # "host:port" or "/path/to.sock" (required)
      network: string             # "tcp" or "unix" (auto-detected if empty)
      document_root: string       # DOCUMENT_ROOT base path (required)
      script_name: string         # fixed entry point e.g. "/index.php" (empty = filesystem mode)
      index: string               # default index file (default "index.php")
      conn_timeout: duration      # connection timeout (default 5s)
      read_timeout: duration      # read timeout (default 30s)
      params: map[string]string   # extra CGI parameters
      pool_size: int              # connection pool size (default 8)
```

**Validation:** `address` and `document_root` are required. `network` must be `tcp` or `unix`. `pool_size` must be >= 0. Mutually exclusive with `echo`, `static`, `sequential`, `aggregate`, `backends`/`service`/`upstream`, and `passthrough`. When enabled, the FastCGI handler replaces the proxy as the innermost handler.

See [FastCGI Proxy](fastcgi.md) for modes, CGI parameters, and examples.

### Passthrough

```yaml
    passthrough: bool           # skip body-processing middleware (default false)
```

**Validation:** Mutually exclusive with `validation`, `compression`, `cache`, `graphql`, `openapi`, `request_decompression`, `response_limit`, and body transforms. Use for binary protocols or zero-overhead routes.

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
      request: duration        # total end-to-end request timeout
      backend: duration        # per-backend-call timeout
      header_timeout: duration # timeout for response headers
      idle: duration           # idle timeout for response body streaming
```

**Validation:** All durations must be >= 0. `backend` must be <= `request` when both are set. `header_timeout` must be <= `backend` (or `request` if no `backend`) when both are set.

### Circuit Breaker

```yaml
    circuit_breaker:
      enabled: bool
      failure_threshold: int    # > 0
      max_requests: int         # > 0 (half-open limit)
      timeout: duration         # > 0 (open → half-open)
      mode: string              # "local" (default) or "distributed" (requires redis)
```

### Cache

```yaml
    cache:
      enabled: bool
      mode: string              # "local" (default) or "distributed" (Redis-backed)
      conditional: bool         # enable ETag/Last-Modified/304 Not Modified support
      ttl: duration             # > 0
      max_size: int             # > 0 (max entries, local mode only)
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
      canary_group: string              # must match a traffic_split group name
      auto_start: bool                  # start progression on route load (default false)
      steps:
        - weight: int                   # 0-100, monotonically non-decreasing
          pause: duration               # hold duration before next step
      analysis:
        error_threshold: float          # 0.0-1.0 (absolute rollback threshold)
        latency_threshold: duration     # max p99 before rollback (absolute)
        max_error_rate_increase: float  # canary/baseline error ratio (0 = disabled)
        max_latency_increase: float     # canary/baseline p99 ratio (0 = disabled)
        max_failures: int               # consecutive failures before rollback (0 = immediate)
        min_requests: int               # min samples before evaluation
        interval: duration              # evaluation frequency (default 30s)
```

**Validation:** Requires `traffic_split`. `canary_group` must exist in traffic splits. At least one step required. Step weights must be 0-100 and monotonically non-decreasing. `error_threshold` must be 0.0-1.0. `max_error_rate_increase`, `max_latency_increase`, and `max_failures` must be >= 0.

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
      algorithms: [string]    # "gzip", "br", "zstd" (default: all three)
      level: int              # 0-11 (default 6; gzip clamped to 9)
      min_size: int           # min bytes to compress (default 1024)
      content_types: [string] # MIME types to compress
```

**Validation:** `algorithms` entries must be `"gzip"`, `"br"`, or `"zstd"`. `level` must be 0-11. `min_size` must be >= 0.

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

### Geo Filtering (per-route)

```yaml
    geo:
      enabled: bool
      inject_headers: bool      # override global
      allow_countries: [string] # replaces global list
      deny_countries: [string]
      allow_cities: [string]
      deny_cities: [string]
      order: string             # "deny_first" (default) or "allow_first"
      shadow_mode: bool
```

Note: The `database` field is only valid at the global level. Per-route geo config inherits the global provider.

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
        detailed_diff: bool          # enable field-level diff (requires enabled)
        max_body_capture: int        # max bytes to buffer for diff, default 1MiB
        max_mismatches: int          # ring buffer capacity, default 100
        ignore_headers: [string]     # headers to exclude from comparison
        ignore_json_fields: [string] # gjson paths to ignore in JSON body diff
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
      type: string            # "http_to_grpc", "http_to_thrift", or "grpc_to_rest"
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
      thrift:
        idl_file: string      # path to .thrift IDL file (mutually exclusive with methods)
        service: string       # Thrift service name (required)
        method: string        # fixed method name (optional)
        timeout: duration     # default 30s
        protocol: string      # "binary" (default) or "compact"
        transport: string     # "framed" (default) or "buffered"
        multiplexed: bool     # enable TMultiplexedProtocol
        tls:
          enabled: bool
          cert_file: string
          key_file: string
          ca_file: string
        mappings:
          - http_method: string    # GET, POST, PUT, DELETE, PATCH
            http_path: string      # /path/:param or /path/{param}
            thrift_method: string
            body: string           # "", "*", or "field_name"
        methods:               # inline method definitions (mutually exclusive with idl_file)
          MethodName:
            args:
              - id: int32          # Thrift field ID (> 0)
                name: string       # field name
                type: string       # bool, byte, i16, i32, i64, double, string, binary, struct, list, set, map, or enum name
                struct: string     # struct name (when type=struct)
                elem: string       # element type (when type=list/set)
                key: string        # key type (when type=map)
                value: string      # value type (when type=map)
            result:                # field 0 = success return, 1+ = exceptions
              - id: int32          # 0 for success, 1+ for exceptions
                name: string
                type: string
                struct: string
            oneway: bool           # fire-and-forget (no response)
            void: bool             # no return value
        structs:               # inline struct definitions
          StructName:
            - id: int32
              name: string
              type: string
              struct: string
              elem: string
              key: string
              value: string
        enums:                 # inline enum definitions
          EnumName:
            VALUE_NAME: int    # enum value name → integer value
      rest:
        timeout: duration           # per-call timeout (default 30s)
        descriptor_files: [string]  # paths to .pb descriptor set files (for protobuf ↔ JSON)
        mappings:
          - grpc_service: string    # fully-qualified gRPC service name (required)
            grpc_method: string     # gRPC method name (required)
            http_method: string     # GET, POST, PUT, DELETE, PATCH (required)
            http_path: string       # REST path with {variables} (required)
            body: string            # "*" = send full body as JSON, "" = no body (query params only)
```

**Validation (gRPC):** Mutually exclusive with `grpc.enabled`. `method` and `mappings` are mutually exclusive. If `grpc.tls.enabled` is true, `ca_file` is required. If `mappings` is used, `service` is required. `method` requires `service`.

**Validation (Thrift):** `idl_file` and `methods` are mutually exclusive; one must be provided. `service` is required. `method` and `mappings` are mutually exclusive. `protocol` must be `binary` or `compact`. `transport` must be `framed` or `buffered`. If `tls.enabled` is true, `ca_file` is required. When using `methods`: field IDs in args must be > 0; in result, ID 0 is the success return. Struct references must exist in `structs`. Enum references must exist in `enums`. Enums must have at least one value.

**Validation (gRPC-to-REST):** At least one mapping is required. Each mapping must have `grpc_service`, `grpc_method`, `http_method`, and `http_path`. `http_method` must be GET/POST/PUT/DELETE/PATCH. No duplicate gRPC service/method combinations. Mutually exclusive with `grpc.enabled`.

### gRPC Passthrough

```yaml
    grpc:
      enabled: bool
      deadline_propagation: bool      # parse grpc-timeout and set context deadline
      max_recv_msg_size: int          # max request body in bytes (0=unlimited)
      max_send_msg_size: int          # max response body in bytes (0=unlimited)
      authority: string               # override :authority pseudo-header
      metadata_transforms:
        request_map: map[string]string  # HTTP header → gRPC metadata
        response_map: map[string]string # gRPC metadata → HTTP header
        strip_prefix: string            # auto-strip prefix from headers
        passthrough: [string]           # headers to pass as-is
      health_check:
        enabled: bool                   # use grpc.health.v1 instead of HTTP
        service: string                 # service name (empty = overall)
```

**Validation:** `max_recv_msg_size` and `max_send_msg_size` must be >= 0. `grpc.enabled` is mutually exclusive with `protocol` translation.

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
      request_queue:
        enabled: bool
        max_depth: int            # default 100, max queued requests
        max_wait: duration        # default 30s, max queue wait time
```

### A/B Testing (per-route)

```yaml
    ab_test:
      enabled: bool
      experiment_name: string     # required when enabled
```

Requires `traffic_split` to be configured. Mutually exclusive with `canary` and `blue_green`.

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

### Error Pages

```yaml
    error_pages:
      enabled: bool
      pages:
        "404":                        # exact status code
          json: string                # inline Go template
          json_file: string           # path to template file
          html: string
          html_file: string
          xml: string
          xml_file: string
        "5xx":                        # class pattern (5xx = 500-599)
          json: '{"error":"server error","code":{{.StatusCode}}}'
        "default":                    # fallback for unmatched codes
          html: '<h1>{{.StatusCode}} {{.StatusText}}</h1>'
          json: '{"error":"{{.StatusText}}"}'
```

Also available at the top level (`error_pages:`) for global defaults. Per-route keys override global keys; unmatched global keys are inherited.

**Template variables:** `{{.StatusCode}}`, `{{.StatusText}}`, `{{.ErrorMessage}}`, `{{.RequestID}}`, `{{.RequestMethod}}`, `{{.RequestPath}}`, `{{.Host}}`, `{{.Timestamp}}`, `{{.RouteID}}`

**Content negotiation:** Format selected from the `Accept` header (`text/html` → html, `application/json` → json, `application/xml` / `text/xml` → xml). Defaults to JSON. Falls back to best available format.

**Fallback chain:** exact status code → class pattern (e.g. `4xx`) → `default` → pass through.

**Validation:** Keys must be exact status codes (100-599), class patterns (`1xx`-`5xx`), or `"default"`. Inline and file are mutually exclusive per format. At least one format required per entry. Templates must parse. File paths must exist.

See [Error Pages](error-pages.md) for full documentation.

---

### Nonce (Replay Prevention)

```yaml
    nonce:
      enabled: bool
      header: string           # nonce header name (default "X-Nonce")
      query_param: string      # optional query parameter name (e.g. "nonce")
      ttl: duration            # how long nonces are remembered (default 5m)
      mode: string             # "local" (default) or "distributed"
      scope: string            # "global" (default) or "per_client"
      required: bool           # reject missing nonce (default true)
      timestamp_header: string # optional timestamp header for age validation
      max_age: duration        # max request age (requires timestamp_header)
```

Per-route nonce config is merged with the global `nonce:` block. Per-route fields override global fields.

**Validation:** `mode` must be `local` or `distributed`. `scope` must be `global` or `per_client`. `ttl` and `max_age` must be >= 0. `max_age > 0` requires `timestamp_header`. `mode: "distributed"` requires `redis.address`.

See [Replay Prevention](replay-prevention.md) for full documentation.

### Outlier Detection

```yaml
    outlier_detection:
      enabled: bool              # enable per-backend outlier detection (default false)
      interval: duration         # detection evaluation frequency (default 10s)
      window: duration           # sliding window for metrics (default 30s)
      min_requests: int          # minimum samples before evaluation (default 10)
      error_rate_threshold: float # absolute error rate threshold, 0.0-1.0 (default 0.5)
      error_rate_multiplier: float # multiplier vs median error rate (default 2.0)
      latency_multiplier: float  # p99 multiplier vs median p99 (default 3.0)
      base_ejection_duration: duration # initial ejection duration (default 30s)
      max_ejection_duration: duration  # max ejection duration (default 5m)
      max_ejection_percent: float # max % of backends to eject, 0-100 (default 50)
```

**Validation:** `interval`, `window`, `base_ejection_duration`, `max_ejection_duration` must be >= 0. `error_rate_threshold` must be 0.0-1.0. `error_rate_multiplier`, `latency_multiplier` must be >= 0. `max_ejection_percent` must be 0-100. `max_ejection_duration` must be >= `base_ejection_duration` when both are > 0.

See [Resilience](resilience.md#outlier-detection) for full documentation.

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

### Geo Filtering (global)

```yaml
geo:
  enabled: bool             # enable geo filtering
  database: string          # path to .mmdb or .ipdb file (required when enabled)
  inject_headers: bool      # inject X-Geo-Country / X-Geo-City headers
  allow_countries: [string] # ISO 3166-1 alpha-2 codes (e.g., "US", "DE")
  deny_countries: [string]
  allow_cities: [string]    # case-insensitive city names
  deny_cities: [string]
  order: string             # "deny_first" (default) or "allow_first"
  shadow_mode: bool         # log but don't reject
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

**Geo fields:** When `geo.enabled: true` is configured, the `geo.country`, `geo.country_name`, and `geo.city` fields are available in rule expressions (e.g., `geo.country in ["US", "CA"]`). See [Rules Engine](rules-engine.md) for details.

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
  request_queue:
    enabled: bool
    max_depth: int            # default 100, max queued requests
    max_wait: duration        # default 30s, max queue wait time
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
  rotation:                 # log file rotation (only applies when output is a file path)
    max_size: int           # max MB before rotation (default 100)
    max_backups: int        # old rotated files to keep (default 3)
    max_age: int            # days to retain old files (default 28)
    compress: bool          # gzip rotated files (default true)
    local_time: bool        # local time in filenames (default false)
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
  pprof: bool               # enable /debug/pprof/* endpoints (default false)
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

---

## Webhooks

```yaml
webhooks:
  enabled: bool               # enable webhook notifications
  timeout: duration           # HTTP request timeout (default 5s)
  workers: int                # worker goroutines (default 4)
  queue_size: int             # event queue capacity (default 1000)
  retry:
    max_retries: int          # retry attempts on failure (default 3)
    backoff: duration         # initial backoff (default 1s)
    max_backoff: duration     # max backoff cap (default 30s)
  endpoints:
    - id: string              # unique endpoint identifier (required)
      url: string             # HTTP/HTTPS URL (required)
      secret: string          # HMAC-SHA256 signing secret
      events: [string]        # event patterns to subscribe to (required)
      headers:                # custom HTTP headers
        X-Custom: value
      routes: [string]        # restrict to specific route IDs
```

**Validation:**
- `enabled: true` requires at least one endpoint
- Each endpoint must have a unique `id`, a valid `url` (http/https), and non-empty `events`
- Valid event prefixes: `backend.`, `circuit_breaker.`, `canary.`, `config.`, or `*`
- `retry.max_backoff` must be >= `retry.backoff` when both are set

See [Webhooks](webhooks.md) for event types and payload format.

---

## Error Pages (global)

```yaml
error_pages:
  enabled: bool
  pages:
    "404":
      json: '{"error":"not found","code":{{.StatusCode}}}'
      html: '<h1>{{.StatusCode}} {{.StatusText}}</h1>'
    "5xx":
      json: '{"error":"server error"}'
    "default":
      json: '{"error":"{{.StatusText}}"}'
```

Global error pages provide defaults for all routes. Per-route `error_pages` override global entries by key.

See [Error Pages](error-pages.md) for template variables, content negotiation, and fallback chain.

---

## Nonce (global)

```yaml
nonce:
  enabled: bool
  header: string           # nonce header name (default "X-Nonce")
  query_param: string      # optional query parameter name (e.g. "nonce")
  ttl: duration            # how long nonces are remembered (default 5m)
  mode: string             # "local" (default) or "distributed"
  scope: string            # "global" (default) or "per_client"
  required: bool           # reject missing nonce (default true)
  timestamp_header: string # optional timestamp header for age validation
  max_age: duration        # max request age (requires timestamp_header)
```

Global nonce config provides defaults for all routes. Per-route `nonce` fields override global fields.

See [Replay Prevention](replay-prevention.md) for storage modes, scoping, and timestamp validation.

---

## CSRF Protection

### Per-Route

```yaml
    csrf:
      enabled: bool
      cookie_name: string             # cookie name (default "_csrf")
      header_name: string             # header name (default "X-CSRF-Token")
      secret: string                  # HMAC signing key (required when enabled)
      token_ttl: duration             # token lifetime (default 1h)
      safe_methods: [string]          # methods that skip validation (default GET,HEAD,OPTIONS,TRACE)
      allowed_origins: [string]       # exact origin matches
      allowed_origin_patterns: [string] # regex origin patterns
      cookie_path: string             # cookie path (default "/")
      cookie_domain: string           # cookie domain
      cookie_secure: bool             # Secure flag
      cookie_samesite: string         # strict/lax/none (default "lax")
      cookie_http_only: bool          # HttpOnly flag (default false)
      inject_token: bool              # set token cookie on safe methods
      shadow_mode: bool               # log but don't reject
      exempt_paths: [string]          # glob patterns that skip CSRF checks
```

Per-route CSRF config is merged with the global `csrf:` block. Per-route fields override global fields.

### Global

```yaml
csrf:
  enabled: bool
  cookie_name: string
  header_name: string
  secret: string
  token_ttl: duration
  safe_methods: [string]
  allowed_origins: [string]
  allowed_origin_patterns: [string]
  cookie_path: string
  cookie_domain: string
  cookie_secure: bool
  cookie_samesite: string
  cookie_http_only: bool
  inject_token: bool
  shadow_mode: bool
  exempt_paths: [string]
```

Global CSRF config provides defaults for all routes. Per-route `csrf` fields override global fields.

**Validation:** `secret` required when enabled. `cookie_samesite` must be `strict`, `lax`, or `none`. `cookie_samesite: "none"` requires `cookie_secure: true`. `token_ttl` must be >= 0. `allowed_origin_patterns` must be valid regex.

See [CSRF Protection](csrf.md) for token lifecycle, client integration, and security considerations.

---

## Idempotency Key

```yaml
idempotency:
  enabled: bool               # enable idempotency key support (default false)
  header_name: string          # header to read key from (default "Idempotency-Key")
  ttl: duration                # how long to store responses (default 24h)
  methods: [string]            # HTTP methods to check (default ["POST","PUT","PATCH"])
  enforce: bool                # reject mutations without key with 422 (default false)
  key_scope: string            # "global" or "per_client" (default "global")
  mode: string                 # "local" or "distributed" (default "local")
  max_key_length: int          # max key length, 400 if exceeded (default 256)
  max_body_size: int64         # max response body to store in bytes (default 1048576)
```

Per-route idempotency config is merged with the global `idempotency:` block. Per-route fields override global fields.

**Validation:** `mode` must be `local` or `distributed`. `key_scope` must be `global` or `per_client`. `ttl`, `max_key_length`, `max_body_size` must be >= 0. `methods` must be valid HTTP methods. `mode: "distributed"` requires `redis.address`.

See [Idempotency Key Support](idempotency.md) for detailed usage, scoping, and examples.

---

## Backend Signing

```yaml
backend_signing:
  enabled: bool               # enable HMAC request signing (default false)
  algorithm: string           # "hmac-sha256" (default) or "hmac-sha512"
  secret: string              # base64-encoded shared secret (min 32 decoded bytes)
  key_id: string              # key identifier for rotation (required)
  signed_headers: [string]    # request headers to include in signature
  include_body: bool          # hash request body into signature (default true)
  header_prefix: string       # prefix for injected headers (default "X-Gateway-")
```

Per-route backend signing config is merged with the global `backend_signing:` block. Per-route fields override global fields.

**Validation:** `algorithm` must be `hmac-sha256` or `hmac-sha512`. `secret` must be valid base64 decoding to at least 32 bytes. `key_id` is required. `signed_headers` must not contain whitespace. `header_prefix` must not contain whitespace.

See [Security](security.md#backend-request-signing) for signing protocol details and backend verification.

---

## Request Decompression

```yaml
request_decompression:
  enabled: bool               # enable request body decompression (default false)
  algorithms: [string]        # "gzip", "deflate", "br", "zstd" (default: all four)
  max_decompressed_size: int  # max decompressed body size in bytes (default 52428800 = 50MB)
```

Per-route request decompression config is merged with the global `request_decompression:` block. Per-route fields override global fields.

**Validation:** `algorithms` must contain only valid values (`gzip`, `deflate`, `br`, `zstd`). `max_decompressed_size` must be >= 0.

See [Transformations](transformations.md#request-decompression) for details.

---

## Security Response Headers

```yaml
security_headers:
  enabled: bool                           # enable automatic security headers (default false)
  strict_transport_security: string       # HSTS value, e.g. "max-age=31536000; includeSubDomains"
  content_security_policy: string         # CSP header value
  x_content_type_options: string          # default "nosniff" (always injected when enabled)
  x_frame_options: string                 # "DENY", "SAMEORIGIN", or "ALLOW-FROM uri"
  referrer_policy: string                 # e.g. "strict-origin-when-cross-origin"
  permissions_policy: string              # e.g. "camera=(), microphone=()"
  cross_origin_opener_policy: string      # e.g. "same-origin"
  cross_origin_embedder_policy: string    # e.g. "require-corp"
  cross_origin_resource_policy: string    # e.g. "same-origin"
  x_permitted_cross_domain_policies: string # e.g. "none"
  custom_headers:                         # arbitrary extra response headers
    Header-Name: "value"
```

Per-route security headers config is merged with the global `security_headers:` block. Per-route non-empty fields override global fields. `custom_headers` maps are merged (per-route keys override global keys with the same name).

**Validation:** `custom_headers` keys must not be empty strings.

See [Security](security.md#security-response-headers) for details.

---

## Response Size Limiting

```yaml
response_limit:
  enabled: bool                # enable response size limiting (default false)
  max_size: int                # maximum response body size in bytes (required when enabled)
  action: string               # "reject" (default), "truncate", "log_only"
```

Per-route `response_limit:` overrides global. Per-route non-zero fields override global fields.

**Actions:**
- `reject` — If the backend response has a `Content-Length` header exceeding `max_size`, returns 502 Bad Gateway immediately. For streaming (chunked) responses, writes are accepted until the limit is reached, then discarded silently.
- `truncate` — Writes up to `max_size` bytes, then discards the rest. The client receives a truncated response.
- `log_only` — Passes the full response through but increments the `limited` counter in metrics. Useful for monitoring before enforcing.

**Validation:** `max_size` must be > 0 when enabled. `action` must be one of: `reject`, `truncate`, `log_only`.

When a response is limited, the `X-Response-Limited: true` header is set.

---

## Maintenance Mode

```yaml
maintenance:
  enabled: bool                # enable maintenance mode (default false)
  status_code: int             # HTTP status code (default 503)
  body: string                 # response body (default JSON error message)
  content_type: string         # Content-Type header (default "application/json")
  retry_after: string          # Retry-After header value (seconds or HTTP-date)
  exclude_paths: [string]      # glob patterns for paths that bypass maintenance
  exclude_ips: [string]        # IPs or CIDRs that bypass maintenance
  headers:                     # extra response headers
    Header-Name: "value"
```

Per-route maintenance config is merged with the global `maintenance:` block. Per-route non-empty fields override global fields. Maintenance mode can be toggled at runtime via admin API without config reload.

**Validation:** `status_code` must be 100-599. `exclude_ips` entries must be valid IPs or CIDRs.

See [Resilience](resilience.md#maintenance-mode) for details.

---

## Health Check

```yaml
health_check:
  path: string              # health check path (default "/health")
  method: string            # HTTP method: GET, HEAD, OPTIONS, POST (default "GET")
  interval: duration        # check interval (default 10s)
  timeout: duration         # per-check timeout (default 5s)
  healthy_after: int        # consecutive successes to mark healthy (default 2)
  unhealthy_after: int      # consecutive failures to mark unhealthy (default 3)
  expected_status: [string] # status patterns considered healthy (default ["200-399"])
```

Status patterns: `"200"` (exact code), `"2xx"` (class), `"200-299"` (range).

Per-backend overrides can be set on each `backends[].health_check` entry. Unset fields inherit from the global config.

**Validation:** `method` must be GET, HEAD, OPTIONS, or POST. `timeout` must be <= `interval` when both > 0. All durations >= 0. `healthy_after` and `unhealthy_after` >= 0. Status patterns must be valid.

See [Resilience](resilience.md#health-checks) for full documentation.

---

## Transport

Global upstream transport settings (connection pooling, timeouts, TLS). These defaults apply to all upstreams; per-upstream overrides are set in `upstreams.<name>.transport`.

```yaml
transport:
  max_idle_conns: int              # max total idle connections across all hosts (default 100)
  max_idle_conns_per_host: int     # max idle connections per upstream host (default 10)
  max_conns_per_host: int          # max total connections per host, 0 = unlimited (default 0)
  idle_conn_timeout: duration      # close idle connections after this duration (default 90s)
  dial_timeout: duration           # TCP dial timeout (default 30s)
  tls_handshake_timeout: duration  # TLS handshake timeout (default 10s)
  response_header_timeout: duration # timeout waiting for response headers, 0 = none (default 0)
  expect_continue_timeout: duration # timeout for 100-continue from server (default 1s)
  disable_keep_alives: bool        # disable HTTP keep-alive (default false)
  insecure_skip_verify: bool       # skip TLS certificate verification (default false)
  ca_file: string                  # path to custom CA certificate file
  cert_file: string                # path to client certificate for upstream mTLS
  key_file: string                 # path to client private key for upstream mTLS
  force_http2: bool                # attempt HTTP/2 connections (default true)
  enable_http3: bool               # connect via HTTP/3 over QUIC (mutually exclusive with force_http2)
```

**Three-level merge:** defaults (hardcoded) -> global `transport:` -> per-upstream `upstreams.<name>.transport:`. Non-zero values at each level override the previous level.

**Validation:** All integer fields >= 0. All durations >= 0. If `ca_file` is set, the file must exist. `cert_file` and `key_file` must both be set if either is specified, and both files must exist.

See [Transport](transport.md) for tuning guidance.

## Shutdown

Graceful shutdown and connection draining settings.

```yaml
shutdown:
  timeout: duration       # total shutdown timeout (default 30s)
  drain_delay: duration   # delay before stopping listeners for LB deregistration (default 0s)
```

**Validation:** Both durations >= 0. `drain_delay` must be less than `timeout` when both are set.

See [Graceful Shutdown](graceful-shutdown.md) for Kubernetes integration and drain API.

## Trusted Proxies

Configure trusted proxy CIDRs for secure real client IP extraction from forwarded headers.

```yaml
trusted_proxies:
  cidrs:                     # trusted proxy CIDRs or bare IPs
    - "10.0.0.0/8"
    - "172.16.0.0/12"
    - "192.168.0.0/16"
    - "127.0.0.1"
  headers:                   # headers to check for client IP (default: X-Forwarded-For, X-Real-IP)
    - "X-Forwarded-For"
    - "X-Real-IP"
  max_hops: int              # max proxy hops to walk back in XFF chain, 0 = unlimited (default 0)
```

**Validation:** All `cidrs` entries must be valid CIDR or IP. `max_hops` >= 0.

See [Security](security.md#trusted-proxies) for how IP extraction works and its security impact.

## Bot Detection (global)

```yaml
bot_detection:
  enabled: bool
  deny: [string]            # regex patterns to block
  allow: [string]           # regex patterns to allow (bypass deny)
```

Per-route `bot_detection` config overrides the global block when both are enabled.

**Validation:** At least one `deny` pattern required when enabled. All patterns must be valid Go regexes.

See [Bot Detection](bot-detection.md) for details.

## Client mTLS (global)

```yaml
client_mtls:
  enabled: bool
  client_auth: string             # "request"|"require"|"verify" (default "verify")
  client_ca_file: string          # path to CA PEM file
  client_cas: [string]            # paths to multiple CA PEM files
  allow_expired: bool             # skip expiry check (testing only)
```

Per-route `client_mtls` config overrides the global block when both are enabled.

**Validation:** `client_auth` must be `"request"`, `"require"`, or `"verify"`. Verify mode requires `client_ca_file` or `client_cas`. All CA files must exist.

See [Client mTLS](client-mtls.md) for details.

## HTTPS Redirect (global)

```yaml
https_redirect:
  enabled: bool            # enable HTTP→HTTPS redirect (default false)
  port: int                # target HTTPS port (default 443)
  permanent: bool          # true=301, false=302 (default false)
```

**Validation:** `port` must be 0-65535 when set.

See [Security](security.md#https-redirect) for details.

## Allowed Hosts (global)

```yaml
allowed_hosts:
  enabled: bool            # enable host validation (default false)
  hosts: [string]          # exact or "*.example.com" wildcard hosts
```

**Validation:** At least one host required when enabled. No empty strings.

See [Security](security.md#allowed-hosts) for details.

## Token Revocation (global)

```yaml
token_revocation:
  enabled: bool            # enable token revocation (default false)
  mode: string             # "local" (default) or "distributed"
  default_ttl: duration    # max revocation TTL (default 24h)
```

**Validation:** Mode must be `local` or `distributed`. Distributed mode requires `redis.address`.

See [Authentication](authentication.md#token-revocation) for details.

## Claims Propagation (per-route)

```yaml
claims_propagation:
  enabled: bool                  # enable claims propagation (default false)
  claims: map[string]string      # claim_name -> header_name
```

**Validation:** At least one claim mapping required when enabled. No empty claim names or header names.

See [Authentication](authentication.md#claims-propagation) for details.

## Service Rate Limit (global)

```yaml
service_rate_limit:
  enabled: bool            # enable service-level rate limiting (default false)
  rate: int                # requests per period (required when enabled)
  period: duration         # time window (default 1s)
  burst: int               # burst capacity (default = rate)
```

**Validation:** `rate` must be > 0 when enabled.

See [Service Rate Limiting](service-rate-limiting.md) for details.

## Spike Arrest (global + per-route)

```yaml
# Global defaults
spike_arrest:
  enabled: bool            # enable spike arrest (default false)
  rate: int                # max requests per period
  period: duration         # time window (default 1s)
  burst: int               # burst capacity (default = rate)
  per_ip: bool             # per-client-IP tracking (default false)

# Per-route (same fields, overrides global)
routes:
  - id: example
    spike_arrest:
      enabled: bool
      rate: int
      period: duration
      burst: int
      per_ip: bool
```

**Validation:** `rate` must be > 0 when enabled.

See [Spike Arrest](spike-arrest.md) for details.

## Content Replacer (per-route)

```yaml
routes:
  - id: example
    content_replacer:
      enabled: bool                    # enable content replacement (default false)
      replacements:
        - pattern: string              # Go regex pattern (required)
          replacement: string          # replacement string ($1, $2 groups)
          scope: string                # "body" (default) or "header:<name>"
```

**Validation:** At least one replacement required when enabled. All patterns must be valid Go regexes. Mutually exclusive with `passthrough`.

See [Content Replacer](content-replacer.md) for details.

## Follow Redirects (per-route)

```yaml
routes:
  - id: example
    follow_redirects:
      enabled: bool              # enable following backend redirects (default false)
      max_redirects: int         # maximum redirect hops (default 10)
```

**Validation:** `max_redirects` must be >= 0 when enabled.

See [Follow Redirects](follow-redirects.md) for details.

## Body Generator (per-route)

```yaml
routes:
  - id: example
    body_generator:
      enabled: bool              # enable body generation (default false)
      template: string           # Go text/template string (required)
      content_type: string       # Content-Type for generated body (default "application/json")
```

**Validation:** `template` is required when enabled and must be a valid Go template. Mutually exclusive with `passthrough`.

See [Body Generator](body-generator.md) for details.

## Sequential Proxy (per-route)

```yaml
routes:
  - id: example
    sequential:
      enabled: bool              # enable sequential proxy (default false)
      steps:                     # ordered backend steps (min 2 required)
        - url: string            # Go template for backend URL (required)
          method: string         # HTTP method (default GET)
          headers:               # map of header name → Go template value
            Header-Name: string
          body_template: string  # Go template for request body
          timeout: duration      # per-step timeout (default 5s)
```

**Validation:** At least 2 steps required. Each step must have a `url`. Mutually exclusive with `echo`, `static`, and `passthrough`. No `backends` required.

See [Sequential Proxy](sequential-proxy.md) for details.

## Quota (per-route)

```yaml
routes:
  - id: example
    quota:
      enabled: bool              # enable quota enforcement (default false)
      limit: int                 # max requests per period (required, > 0)
      period: string             # "hourly", "daily", "monthly", or "yearly" (required)
      key: string                # client key: "ip", "client_id", "header:<name>", "jwt_claim:<name>" (required)
      redis: bool                # use Redis for distributed counting (default false)
```

**Validation:** `limit` must be > 0. `period` must be one of `hourly`, `daily`, `monthly`, `yearly`. `key` must be a valid key format.

See [Quota](quota.md) for details.

## Multi-Tenancy (global)

```yaml
tenants:
  enabled: bool                  # enable multi-tenancy (default false)
  key: string                    # tenant ID key: "header:<name>", "jwt_claim:<name>", "client_id" (required)
  default_tenant: string         # fallback tenant ID (empty = reject unknown)
  tenants:                       # tenant definitions (at least one required)
    <tenant-id>:
      rate_limit:
        rate: int                # requests per period (> 0)
        period: duration         # rate limit window
        burst: int               # max burst (defaults to rate)
      quota:
        limit: int               # max requests per period (> 0)
        period: string           # "hourly", "daily", "monthly", "yearly"
      routes: [string]           # allowed route IDs (empty = all)
      metadata:                  # key-value pairs propagated as X-Tenant-* headers
        <key>: <value>
```

**Per-route:**

```yaml
routes:
  - id: example
    tenant:
      required: bool             # reject if no tenant resolved (default false)
      allowed: [string]          # restrict to specific tenant IDs
```

**Validation:** `key` must be `client_id`, `header:<name>`, or `jwt_claim:<name>`. At least one tenant must be defined. `default_tenant` must reference an existing tenant. Per-tenant rate limit rate must be > 0. Per-tenant quota limit must be > 0 with valid period. `tenant.required` requires global tenants enabled. `tenant.allowed` IDs must exist in tenants map.

See [Multi-Tenancy](multi-tenancy.md) for details.

## Response Aggregation (per-route)

```yaml
routes:
  - id: example
    aggregate:
      enabled: bool              # enable response aggregation (default false)
      timeout: duration          # global timeout for all backends (default 5s)
      fail_strategy: string      # "abort" (default) or "partial"
      backends:
        - name: string           # unique backend name (required)
          url: string            # URL template (required, Go text/template)
          method: string         # HTTP method (default GET)
          headers:               # header templates (optional)
            Header-Name: string
          group: string          # wrap response under this JSON key (optional)
          required: bool         # abort if fails even in partial mode (default false)
          timeout: duration      # per-backend timeout override (optional)
```

**Validation:** Requires ≥ 2 backends. Each backend needs `name` and `url`. Names must be unique. `fail_strategy` must be `abort` or `partial`. Mutually exclusive with `echo`, `sequential`, `static`, `passthrough`.

See [Response Aggregation](response-aggregation.md) for details.

## Response Body Generator (per-route)

```yaml
routes:
  - id: example
    response_body_generator:
      enabled: bool              # enable response body generation (default false)
      template: string           # Go text/template string (required)
      content_type: string       # Content-Type for generated response (default "application/json")
```

**Validation:** `template` is required when enabled. Mutually exclusive with `passthrough`.

See [Response Body Generator](response-body-generator.md) for details.

## Parameter Forwarding (per-route)

```yaml
routes:
  - id: example
    param_forwarding:
      enabled: bool              # enable parameter forwarding control (default false)
      headers:                   # allowed request headers (case-insensitive)
        - string
      query_params:              # allowed query parameter names
        - string
      cookies:                   # allowed cookie names
        - string
```

**Validation:** At least one of `headers`, `query_params`, or `cookies` must be non-empty when enabled.

See [Parameter Forwarding](parameter-forwarding.md) for details.

## Content Negotiation (per-route)

```yaml
routes:
  - id: example
    content_negotiation:
      enabled: bool              # enable content negotiation (default false)
      supported:                 # supported formats: "json", "xml", "yaml"
        - string
      default: string            # default format (default "json")
```

**Validation:** Each supported format must be `json`, `xml`, or `yaml`. Default must be a valid format. Mutually exclusive with `passthrough`.

See [Content Negotiation](content-negotiation.md) for details.

## Response Flatmap & Target (per-route)

```yaml
routes:
  - id: example
    transform:
      response:
        body:
          target: string           # gjson path to extract as root response
          flatmap:                  # array manipulation operations
            - type: string         # "move", "del", "extract", "flatten", "append"
              args:                # operation-specific arguments
                - string
```

**Validation:** Flatmap `type` must be one of `move`, `del`, `extract`, `flatten`, `append`. `move` and `extract` require 2 args, `del` and `flatten` require 1, `append` requires at least 2.

See [Response Flatmap](response-flatmap.md) for details.

## Shared Cache Buckets (per-route)

```yaml
routes:
  - id: example
    cache:
      enabled: true
      bucket: string             # named shared cache bucket
```

**Validation:** Bucket name must be alphanumeric with hyphens/underscores.

See [Shared Cache Buckets](shared-cache-buckets.md) for details.

## CDN Cache Headers (global + per-route)

```yaml
cdn_cache_headers:
  enabled: bool                    # enable CDN cache header injection (default false)
  cache_control: string            # Cache-Control value
  vary:                            # Vary header values
    - string
  surrogate_control: string        # Surrogate-Control header
  surrogate_key: string            # Surrogate-Key header
  expires: string                  # duration ("1h") or HTTP-date
  stale_while_revalidate: int      # seconds, appended to Cache-Control
  stale_if_error: int              # seconds, appended to Cache-Control
  override: bool                   # override backend's Cache-Control (default true)
```

**Validation:** When enabled, at least one of `cache_control`, `surrogate_control`, or `vary` must be set.

See [CDN Cache Headers](cdn-cache-headers.md) for details.

## Backend Encoding (per-route)

```yaml
routes:
  - id: example
    backend_encoding:
      encoding: string             # "xml" or "yaml"
```

**Validation:** Must be `xml` or `yaml`. Mutually exclusive with `passthrough`.

See [Backend Encoding](backend-encoding.md) for details.

---

### Server-Sent Events (SSE) Proxy

```yaml
    sse:
      enabled: bool                    # enable SSE proxy mode (default false)
      heartbeat_interval: duration     # send `: heartbeat\n\n` on idle (0 = disabled)
      retry_ms: int                    # inject `retry:` field on connect (0 = don't inject)
      connect_event: string            # event data to send on connect (empty = none)
      disconnect_event: string         # event data to send on disconnect (empty = none)
      max_idle: duration               # close connection after idle (0 = no limit)
      forward_last_event_id: bool      # forward Last-Event-ID header to backend (default true)
      fanout:
        enabled: bool                  # enable fan-out mode (default false)
        buffer_size: int               # ring buffer size for catch-up events (default 256)
        client_buffer_size: int        # per-client channel buffer (default 64)
        reconnect_delay: duration      # upstream reconnect delay (default 1s)
        max_reconnects: int            # max upstream reconnection attempts (0 = unlimited)
        event_filtering: bool          # allow clients to filter events by type (default false)
        filter_param: string           # query parameter for event type filtering (default "event_type")
```

**Validation:** `heartbeat_interval`, `retry_ms`, and `max_idle` must be >= 0. Mutually exclusive with `passthrough` and `response_body_generator`. Fan-out requires `sse.enabled: true`. `fanout.buffer_size`, `fanout.client_buffer_size`, and `fanout.max_reconnects` must be >= 0. `fanout.reconnect_delay` must be >= 0.

See [SSE Proxy](sse-proxy.md) for streaming patterns and connection lifecycle.

## Debug Endpoint (global)

```yaml
debug_endpoint:
  enabled: bool            # enable debug endpoint (default false)
  path: string             # URL path prefix (default /__debug)
```

**Validation:** `path` must start with `/` when specified.

See [Debug Endpoint](debug-endpoint.md) for details.

---

## Retry Budget Pools (global)

```yaml
retry_budgets:
  pool_name:
    ratio: float       # max retry ratio 0.0-1.0 (exclusive of 0)
    min_retries: int   # min retries per second regardless of ratio (default 0)
    window: duration   # sliding window duration (default 10s)
```

Per-route reference via `retry_policy.budget_pool`:

```yaml
routes:
  - id: my-route
    retry_policy:
      max_retries: 3
      budget_pool: pool_name    # references a named pool above
```

**Validation:** `budget_pool` and inline `budget.ratio` are mutually exclusive. `budget_pool` must reference an existing name in `retry_budgets`. Each pool: `ratio` must be in (0, 1.0], `min_retries` >= 0.

See [Retry Budget Pools](retry-budget-pools.md) for full documentation.

---

## Inbound Signing (global + per-route)

```yaml
inbound_signing:
  enabled: bool            # enable inbound signature verification
  algorithm: string        # "hmac-sha256" or "hmac-sha512" (default "hmac-sha256")
  secret: string           # base64-encoded shared secret (>= 32 bytes)
  key_id: string           # expected key ID (optional)
  signed_headers: [string] # additional headers included in signature
  include_body: bool       # include body hash in signature (default true)
  header_prefix: string    # header name prefix (default "X-Gateway-")
  max_age: duration        # max timestamp age (default 5m)
  shadow_mode: bool        # log failures without rejecting (default false)
```

Per-route config is merged with global. Per-route fields override global.

**Validation:** `algorithm` must be `hmac-sha256` or `hmac-sha512`. `secret` must be valid base64 decoding to >= 32 bytes.

See [Inbound Signing](inbound-signing.md) for full documentation.

---

## PII Redaction (per-route)

```yaml
routes:
  - id: my-route
    pii_redaction:
      enabled: bool          # enable PII redaction
      built_ins: [string]    # "email", "credit_card", "ssn", "phone"
      custom:                # custom regex patterns
        - name: string
          pattern: string    # Go regex
          replacement: string # replacement text (optional; uses mask_char if empty)
      scope: string          # "response" (default), "request", or "both"
      mask_char: string      # mask character (default "*")
      headers: [string]      # response/request headers to redact
```

**Validation:** `built_ins` must be from {email, credit_card, ssn, phone}. Custom patterns must compile. `scope` must be response, request, or both. Mutually exclusive with `passthrough`.

See [PII Redaction](pii-redaction.md) for full documentation.

---

## Field Encryption (per-route)

```yaml
routes:
  - id: my-route
    field_encryption:
      enabled: bool            # enable field-level encryption
      algorithm: string        # "aes-gcm-256" (only supported value)
      key_base64: string       # base64-encoded 32-byte AES key
      encrypt_fields: [string] # gjson paths to encrypt in requests
      decrypt_fields: [string] # gjson paths to decrypt in responses
      encoding: string         # "base64" (default) or "hex"
```

**Validation:** `algorithm` must be `aes-gcm-256`. `key_base64` must decode to exactly 32 bytes. At least one of `encrypt_fields` or `decrypt_fields` must be non-empty. Mutually exclusive with `passthrough`.

See [Field Encryption](field-encryption.md) for full documentation.

---

## Blue-Green Deployments (per-route)

```yaml
routes:
  - id: my-route
    traffic_split:
      - name: blue
        weight: 100
        backends:
          - url: http://blue-v1:8080
      - name: green
        weight: 0
        backends:
          - url: http://green-v2:8080
    blue_green:
      enabled: bool              # enable blue-green deployment
      active_group: string       # name of current active group
      inactive_group: string     # name of standby group
      health_gate:
        min_healthy: int         # min healthy backends before cutover
        timeout: duration        # health gate timeout
      auto_promote_delay: duration # delay before auto-promotion
      rollback_on_error: bool    # auto-rollback on high error rate
      error_threshold: float     # error rate threshold 0.0-1.0
      observation_window: duration # observation duration after promotion
      min_requests: int          # min requests before evaluating error rate
```

**Validation:** Mutually exclusive with `canary.enabled` on the same route. Requires `traffic_split` with groups matching `active_group` and `inactive_group`. `error_threshold` must be in [0, 1.0].

See [Blue-Green Deployments](blue-green.md) for full documentation.

## Load Shedding (global)

```yaml
load_shedding:
  enabled: bool               # enable system-level load shedding (default false)
  cpu_threshold: int           # CPU usage % threshold, 0-100 (default 90)
  memory_threshold: int        # memory usage % threshold, 0-100 (default 85)
  goroutine_limit: int         # max goroutines before shedding, 0 = unlimited (default 0)
  sample_interval: duration    # metric sampling interval (default 1s)
  cooldown_duration: duration  # min shedding duration after activation (default 5s)
  retry_after: int             # Retry-After header value in seconds (default 5)
```

**Validation:** `cpu_threshold` must be 0-100. `memory_threshold` must be 0-100. `goroutine_limit` must be >= 0. `sample_interval` and `cooldown_duration` must be >= 0. `retry_after` must be > 0.

Load shedding runs in the global handler chain after RequestID and before the service rate limit. When any threshold is exceeded, the gateway returns `503 Service Unavailable` with a `Retry-After` header.

See [Load Shedding](load-shedding.md) for details.

---

## Audit Logging (global + per-route)

```yaml
# Global defaults
audit_log:
  enabled: bool               # enable audit logging (default false)
  webhook_url: string          # URL to POST audit events to (required when enabled)
  headers:                     # HTTP headers for webhook requests
    Header-Name: "value"
  sample_rate: float           # fraction of requests to audit, 0.0-1.0 (default 1.0)
  include_body: bool           # capture request/response bodies (default false)
  max_body_size: int           # max body size to capture in bytes (default 65536 = 64KB)
  buffer_size: int             # internal event buffer capacity (default 1000)
  batch_size: int              # events per webhook batch (default 10)
  flush_interval: duration     # max time between webhook deliveries (default 5s)
  methods: [string]            # HTTP methods to audit (empty = all)
  status_codes: [int]          # HTTP status codes to audit (empty = all)

# Per-route (same fields, overrides global)
routes:
  - id: example
    audit_log:
      enabled: bool
      include_body: bool
      max_body_size: int
      sample_rate: float
      methods: [string]
      status_codes: [int]
```

Per-route config is merged with the global `audit_log:` block. Per-route fields override global fields.

**Validation:** `webhook_url` is required when enabled. `sample_rate` must be 0.0-1.0. `max_body_size` must be >= 0. `buffer_size` must be > 0. `batch_size` must be > 0. `flush_interval` must be > 0. `methods` must be valid HTTP methods. `status_codes` must be valid HTTP status codes (100-599).

See [Audit Logging](audit-logging.md) for webhook payload format, delivery semantics, and examples.

---

## SSRF Protection (global)

```yaml
ssrf_protection:
  enabled: bool                  # enable SSRF protection
  allow_cidrs: [string]          # exempt specific private CIDRs
  block_link_local: bool         # block link-local addresses (default true)
```

**Validation:** `allow_cidrs` entries must be valid CIDR notation.

See [SSRF Protection](ssrf-protection.md) for details.

## Baggage Propagation (per-route)

```yaml
routes:
  - id: my-route
    baggage:
      enabled: bool              # enable baggage propagation (default false)
      tags:                      # list of baggage tag definitions
        - name: string           # header name to inject (required)
          source: string         # source expression (required)
```

Source types: `header:<name>`, `jwt_claim:<name>`, `query:<name>`, `cookie:<name>`, `static:<value>`.

**Validation:** At least one tag required when enabled. Each tag must have `name` and `source`. `source` must use a valid prefix (`header:`, `jwt_claim:`, `query:`, `cookie:`, `static:`). `jwt_claim` sources require auth to be configured on the route.

See [Baggage Propagation](baggage-propagation.md) for details.

---

## Backend Backpressure (per-route)

```yaml
routes:
  - id: my-route
    backpressure:
      enabled: bool              # enable backend backpressure detection (default false)
      status_codes: [int]        # overload status codes (default [429, 503])
      max_retry_after: duration  # max backoff duration (default 60s)
      default_delay: duration    # backoff when no Retry-After header (default 5s)
```

**Validation:** `status_codes` must be valid HTTP status codes (100-599). `max_retry_after` must be > 0. `default_delay` must be > 0. `default_delay` must be <= `max_retry_after`.

See [Backend Backpressure](backpressure.md) for details.

---

## Request Deduplication (per-route)

```yaml
routes:
  - id: my-route
    request_dedup:
      enabled: bool              # enable request dedup
      ttl: duration              # response cache TTL (default 60s)
      include_headers: [string]  # headers to include in fingerprint
      include_body: bool         # include body in fingerprint (default true)
      max_body_size: int         # max body bytes to hash (default 1048576)
      mode: string               # "local" or "distributed" (default "local")
```

**Validation:** `mode` must be `"local"` or `"distributed"`. Distributed mode requires `redis.address`. `ttl` must be >= 0. `max_body_size` must be >= 0.

See [Request Deduplication](request-dedup.md) for details.

## IP Blocklist (global + per-route)

```yaml
ip_blocklist:
  enabled: bool                  # enable IP blocklist
  static: [string]               # always-blocked IPs/CIDRs
  action: string                 # "block" (default) or "log"
  feeds:
    - url: string                # feed URL (required)
      refresh_interval: duration # refresh interval (default 5m)
      format: string             # "text" (default) or "json"
```

**Validation:** `action` must be `"block"` or `"log"`. Static entries must be valid IPs or CIDRs. Feed `url` is required. Feed `format` must be `"text"` or `"json"`. `refresh_interval` must be >= 1s.

See [Dynamic IP Blocklist](ip-blocklist.md) for details.

---

## JMESPath Query (per-route)

```yaml
routes:
  - id: example
    jmespath:
      enabled: bool              # enable JMESPath filtering (default false)
      expression: string         # JMESPath expression (required when enabled)
      wrap_collections: bool     # wrap array results in {"collection": [...]} (default false)
```

**Validation:** `expression` is required when enabled and must be a valid JMESPath expression (compiled at config load time).

See [Data Manipulation](data-manipulation.md#jmespath-query-language) for details.

---

## Field Replacer (per-route)

```yaml
routes:
  - id: example
    field_replacer:
      enabled: bool              # enable field replacement (default false)
      operations:
        - field: string          # gjson path to target field (required)
          type: string           # "regexp", "literal", "upper", "lower", "trim" (required)
          find: string           # pattern/chars to find (required for regexp/literal)
          replace: string        # replacement string (for regexp/literal)
```

**Validation:** At least one operation required when enabled. `type` must be one of: `regexp`, `literal`, `upper`, `lower`, `trim`. `regexp` type operations must have valid Go regex in `find`.

See [Data Manipulation](data-manipulation.md#field-level-content-replacer) for details.

---

## Modifiers (per-route)

```yaml
routes:
  - id: example
    modifiers:
      - type: string             # "header_copy", "header_set", "cookie", "query", "stash", "port" (required)
        from: string             # source header (header_copy)
        to: string               # destination header (header_copy)
        name: string             # header/cookie name
        value: string            # header/cookie value
        domain: string           # cookie domain
        path: string             # cookie path
        max_age: int             # cookie max age
        secure: bool             # cookie secure flag
        http_only: bool          # cookie httponly flag
        same_site: string        # cookie SameSite: "lax", "strict", "none"
        params:                  # query params to add/override (query type)
          key: value
        port: int                # port override (port type)
        scope: string            # "request", "response", "both" (default "both")
        priority: int            # execution priority, higher first (default 0)
        condition:               # optional conditional execution
          type: string           # "header", "cookie", "query", "path_regex"
          name: string           # header/cookie/query param name
          value: string          # optional regex pattern to match
        else:                    # modifier to apply when condition is false
          type: string
          # ... same fields as parent modifier
```

**Validation:** `type` must be a valid modifier type. `header_copy` requires `from` and `to`. `cookie` requires `name`. `query` requires non-empty `params`. `port` requires `port` > 0. Condition `type` must be `header`, `cookie`, `query`, or `path_regex`. Condition `value` must be a valid regex when set.

See [Data Manipulation](data-manipulation.md#martian-style-modifiers) for details.

---

## Error Handling (per-route)

```yaml
routes:
  - id: example
    error_handling:
      mode: string               # "default", "pass_status", "detailed", "message" (default "default")
```

**Validation:** `mode` must be one of: `default`, `pass_status`, `detailed`, `message`.

See [Data Manipulation](data-manipulation.md#error-handling-modes) for details.

---

## Lua Scripting (per-route)

```yaml
routes:
  - id: example
    lua:
      enabled: bool              # enable Lua scripting (default false)
      request_script: string     # Lua code for request phase
      response_script: string    # Lua code for response phase
```

**Validation:** At least one of `request_script` or `response_script` must be provided when enabled. Scripts must be valid Lua syntax (compiled at config load time).

See [Data Manipulation](data-manipulation.md#lua-scripting) for details.

---

## Backend Response (per-route)

```yaml
routes:
  - id: example
    backend_response:
      is_collection: bool        # wrap array responses in object (default false)
      collection_key: string     # key name for wrapping (default "collection")
```

See [Data Manipulation](data-manipulation.md#backend-response-is_collection) for details.

---

## Output Encoding (per-route)

```yaml
routes:
  - id: example
    output_encoding: string      # "json", "xml", "yaml", "json-collection", "string"
```

Overrides Accept-header content negotiation with a config-declared encoding.

See [Data Manipulation](data-manipulation.md#output-encoding) for details.

---

## AWS Lambda Backend (per-route)

```yaml
routes:
  - id: example
    lambda:
      enabled: bool              # enable Lambda backend (default false)
      function_name: string      # AWS Lambda function name or ARN (required)
      region: string             # AWS region (default "us-east-1")
      max_retries: int           # retry attempts for failed invocations (default 0)
```

**Validation:** `function_name` is required when enabled. Mutually exclusive with `backends`, `service`, `upstream`, `echo`, `static`, `fastcgi`, `sequential`, `aggregate`, `amqp`, `pubsub`.

See [AWS Lambda Backend](lambda.md) for details.

---

## AMQP/RabbitMQ Backend (per-route)

```yaml
routes:
  - id: example
    amqp:
      enabled: bool              # enable AMQP backend (default false)
      url: string                # AMQP connection URL (required)
      consumer:
        queue: string            # queue name to consume from
        auto_ack: bool           # auto-acknowledge messages (default false)
      producer:
        exchange: string         # exchange to publish to
        routing_key: string      # routing key for published messages
```

**Validation:** `url` is required when enabled. Mutually exclusive with `backends`, `service`, `upstream`, `echo`, `static`, `fastcgi`, `sequential`, `aggregate`, `lambda`, `pubsub`.

See [AMQP/RabbitMQ Backend](amqp.md) for details.

---

## Go CDK Pub/Sub Backend (per-route)

```yaml
routes:
  - id: example
    pubsub:
      enabled: bool              # enable Pub/Sub backend (default false)
      subscription_url: string   # Go CDK subscription URL
      publish_url: string        # Go CDK topic URL
```

**Validation:** At least one of `subscription_url` or `publish_url` is required when enabled. Mutually exclusive with `backends`, `service`, `upstream`, `echo`, `static`, `fastcgi`, `sequential`, `aggregate`, `lambda`, `amqp`.

See [Go CDK Pub/Sub Backend](pubsub.md) for details.

---

## WASM Runtime (global)

```yaml
wasm:
  runtime_mode: string         # "compiler" (default, AOT) or "interpreter"
  max_memory_pages: int        # per-instance memory limit in 64KB pages (default 256 = 16MB)
```

See [WASM Plugins](wasm-plugins.md) for details.

---

## WASM Plugins (per-route)

```yaml
routes:
  - id: example
    wasm_plugins:
      - enabled: bool             # enable this plugin (default false)
        name: string              # human-readable name for metrics/admin
        path: string              # path to .wasm file (required)
        phase: string             # "request", "response", or "both" (default "both")
        config:                   # arbitrary k/v passed to guest
          key: value
        timeout: duration         # per-invocation timeout (default 5ms)
        pool_size: int            # pre-instantiated instance pool (default 4)
```

**Validation:** `path` is required and must exist. `phase` must be `request`, `response`, or `both`. `timeout` and `pool_size` must be non-negative. Mutually exclusive with `passthrough`.

See [WASM Plugins](wasm-plugins.md) for details.
