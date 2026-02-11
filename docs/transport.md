# Transport & Connection Pooling

The gateway maintains a pool of HTTP transports for proxying requests to backends. Transport settings control connection pooling, timeouts, TLS, and HTTP/2 behavior. These can be tuned globally and per-upstream.

## Configuration

### Global Transport

Applied to all upstreams unless overridden:

```yaml
transport:
  max_idle_conns: 200
  max_idle_conns_per_host: 20
  idle_conn_timeout: 120s
  dial_timeout: 10s
  tls_handshake_timeout: 5s
  force_http2: true
```

### Per-Upstream Transport

Override specific settings for individual upstreams. Only non-zero values override the global config:

```yaml
upstreams:
  high-traffic-api:
    backends:
      - url: http://api-1:8080
      - url: http://api-2:8080
    transport:
      max_idle_conns_per_host: 100
      max_conns_per_host: 200

  legacy-backend:
    backends:
      - url: https://legacy:443
    transport:
      insecure_skip_verify: true
      disable_keep_alives: true
      dial_timeout: 5s
```

### Three-Level Merge

Settings merge in order, with later levels overriding earlier:

1. **Hardcoded defaults** (100 idle conns, 30s dial timeout, etc.)
2. **Global `transport:`** overrides defaults
3. **Per-upstream `transport:`** overrides global

Only non-zero values at each level take effect. For example, setting `max_idle_conns_per_host: 50` on an upstream overrides the global value for that upstream only; all other fields inherit from global.

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `max_idle_conns` | int | 100 | Maximum total idle connections across all hosts |
| `max_idle_conns_per_host` | int | 10 | Maximum idle connections per upstream host |
| `max_conns_per_host` | int | 0 (unlimited) | Maximum total connections per host |
| `idle_conn_timeout` | duration | 90s | Close idle connections after this duration |
| `dial_timeout` | duration | 30s | TCP connection dial timeout |
| `tls_handshake_timeout` | duration | 10s | TLS handshake timeout |
| `response_header_timeout` | duration | 0 (none) | Timeout waiting for response headers |
| `expect_continue_timeout` | duration | 1s | Timeout for 100-continue response |
| `disable_keep_alives` | bool | false | Disable HTTP keep-alive connections |
| `insecure_skip_verify` | bool | false | Skip TLS certificate verification |
| `ca_file` | string | - | Path to custom CA certificate (PEM) |
| `cert_file` | string | - | Path to client certificate for upstream mTLS (PEM) |
| `key_file` | string | - | Path to client private key for upstream mTLS (PEM) |
| `force_http2` | bool | true | Attempt HTTP/2 via ALPN negotiation |

## Tuning Guidance

### High-Throughput APIs

Increase idle connections per host to avoid connection churn:

```yaml
transport:
  max_idle_conns: 500
  max_idle_conns_per_host: 50
```

### Connection-Limited Backends

Cap total connections to protect backends that can't handle many concurrent connections:

```yaml
upstreams:
  database-api:
    backends:
      - url: http://db-api:3000
    transport:
      max_conns_per_host: 20
      max_idle_conns_per_host: 10
```

### Custom CA Certificates

For backends using internal CA-signed certificates:

```yaml
transport:
  ca_file: /etc/gateway/internal-ca.pem
```

### Upstream mTLS (Client Certificates)

For backends that require mutual TLS authentication, provide a client certificate and private key:

```yaml
transport:
  ca_file: /etc/gateway/internal-ca.pem
  cert_file: /etc/gateway/client.crt
  key_file: /etc/gateway/client.key
```

Per-upstream client certificates allow different backends to use different identities:

```yaml
upstreams:
  payment-api:
    backends:
      - url: https://payments.internal:443
    transport:
      cert_file: /etc/gateway/payment-client.crt
      key_file: /etc/gateway/payment-client.key
      ca_file: /etc/gateway/payment-ca.pem
```

Both `cert_file` and `key_file` must be specified together. If only one is set, validation will reject the config.

### Legacy Backends

For backends that don't support keep-alive or have TLS issues:

```yaml
upstreams:
  legacy:
    backends:
      - url: https://old-service:443
    transport:
      disable_keep_alives: true
      insecure_skip_verify: true
      force_http2: false
```

## DNS Resolver

The gateway supports custom DNS resolution for backend addresses, configured separately from transport:

```yaml
dns_resolver:
  nameservers:
    - "10.0.0.53:53"
    - "10.0.0.54:53"
  timeout: 5s
```

The custom resolver applies to all transports (global and per-upstream). Nameservers are queried in round-robin order.

## Hot Reload

Transport pools are rebuilt on config reload (`SIGHUP` or `POST /reload`). The old pool's idle connections are closed after the new pool is installed. In-flight requests complete using their already-established connections.

## Admin API

### GET `/transport`

Returns the active transport configuration:

```bash
curl http://localhost:8081/transport
```

```json
{
  "default": {
    "max_idle_conns": 200,
    "max_idle_conns_per_host": 20,
    "max_conns_per_host": 0,
    "idle_conn_timeout": "2m0s",
    "tls_handshake_timeout": "5s",
    "response_header_timeout": "0s",
    "expect_continue_timeout": "1s",
    "disable_keep_alives": false,
    "force_attempt_http2": true
  },
  "upstreams": {
    "legacy-backend": {
      "insecure_skip_verify": true,
      "disable_keep_alives": true,
      "dial_timeout": 5000000000
    }
  }
}
```

The `default` section shows the effective default transport (after merging hardcoded defaults with global config). The `upstreams` section shows per-upstream overrides as configured.
