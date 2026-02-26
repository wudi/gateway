---
title: "HTTP/3 (QUIC) Support"
sidebar_position: 8
---

The runway supports HTTP/3 over QUIC for both inbound client connections and outbound upstream connections. HTTP/3 provides lower latency connection establishment, improved multiplexing without head-of-line blocking, and built-in connection migration.

## Inbound HTTP/3

HTTP/3 is enabled per-listener via `http.enable_http3`. A QUIC UDP socket runs alongside the existing TCP socket on the same port. Both HTTP/1.1+2 (TCP) and HTTP/3 (UDP) connections are served simultaneously.

### Requirements

- TLS must be enabled (`tls.enabled: true`) — QUIC mandates TLS 1.3
- The port must be reachable over both TCP and UDP

### Configuration

```yaml
listeners:
  - id: main-https
    address: ":443"
    protocol: http
    tls:
      enabled: true
      cert_file: /etc/runway/cert.pem
      key_file: /etc/runway/key.pem
    http:
      enable_http3: true
```

### Alt-Svc Advertisement

When HTTP/3 is enabled, the runway automatically injects an `Alt-Svc` response header on all HTTP/1.x and HTTP/2 responses:

```
Alt-Svc: h3=":443"; ma=2592000
```

This tells browsers and HTTP clients that HTTP/3 is available on the same port. The `ma` (max-age) value is 30 days. HTTP/3 responses do not include this header since the client already knows.

### TLS Certificate Hot Reload

HTTP/3 shares the same `GetCertificate` callback as TCP TLS. When certificates are reloaded via `SIGHUP` or the admin API, both TCP and QUIC connections automatically use the new certificate.

## Outbound HTTP/3

HTTP/3 can be enabled per-upstream to connect to backends over QUIC:

```yaml
upstreams:
  modern-backend:
    backends:
      - url: https://api:443
    transport:
      enable_http3: true
```

### Mutual Exclusion

`enable_http3` and `force_http2` are mutually exclusive on the same transport. The config validator rejects configurations that set both.

### TLS Settings

Outbound HTTP/3 transports respect the same TLS settings as TCP transports:

- `insecure_skip_verify` — skip certificate verification
- `ca_file` — custom CA certificate
- `cert_file` / `key_file` — client certificate for upstream mTLS

## Example: Full Configuration

```yaml
listeners:
  - id: public-https
    address: ":443"
    protocol: http
    tls:
      enabled: true
      cert_file: /etc/runway/cert.pem
      key_file: /etc/runway/key.pem
    http:
      enable_http3: true

upstreams:
  modern-api:
    backends:
      - url: https://api.internal:443
    transport:
      enable_http3: true

  legacy-api:
    backends:
      - url: https://old.internal:443
    transport:
      force_http2: true  # standard TCP + HTTP/2

routes:
  - id: api
    path: /api
    path_prefix: true
    upstream: modern-api

  - id: legacy
    path: /legacy
    path_prefix: true
    upstream: legacy-api
```

## Admin API

### GET `/listeners`

The listener endpoint includes HTTP/3 status:

```json
[
  {
    "id": "public-https",
    "protocol": "http",
    "address": ":443",
    "http3": true
  }
]
```
