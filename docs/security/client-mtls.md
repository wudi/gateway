---
title: "Per-Route Client mTLS Verification"
sidebar_position: 13
---

Per-route client mTLS adds route-level client certificate verification on top of listener-level TLS. This enables different routes to trust different Certificate Authorities, even on the same listener.

## Architecture

Client mTLS verification works in two layers:

1. **Listener layer** (`client_auth: "request"`) — the TLS handshake collects client certificates from all connections without verifying them.
2. **Middleware layer** (`client_mtls` on route) — each route verifies the presented certificate against its own CA pool using `x509.Certificate.Verify()`.

This two-layer approach is necessary because TLS negotiation happens once per connection, before routing. The listener must request certificates from all clients; the middleware then decides per-route whether the certificate is acceptable.

## Listener Prerequisite

For per-route client mTLS to work, the listener must be configured to request client certificates:

```yaml
listeners:
  - id: https
    address: ":8443"
    protocol: http
    tls:
      cert_file: /etc/runway/server.crt
      key_file: /etc/runway/server.key
      client_auth: "request"       # ask for certs, don't verify at TLS level
      client_ca_file: ""           # no listener-level CA verification
```

Use `client_auth: "request"` (not `"require"`) if some routes should allow connections without client certificates.

## Configuration

### Per-Route

```yaml
routes:
  - id: payments
    path: /payments
    backends:
      - url: https://payments.internal:443
    client_mtls:
      enabled: true
      client_auth: "verify"              # default: verify cert against CA pool
      client_ca_file: /etc/runway/payment-ca.pem
      # client_cas:                      # multiple CA files
      #   - /etc/runway/ca1.pem
      #   - /etc/runway/ca2.pem
      # allow_expired: false             # skip expiry check (testing only)

  - id: internal
    path: /internal
    backends:
      - url: https://internal.svc:443
    client_mtls:
      enabled: true
      client_ca_file: /etc/runway/internal-ca.pem

  - id: public
    path: /public
    backends:
      - url: https://public.svc:443
    # No client_mtls — accessible without client certificate
```

### Global

```yaml
client_mtls:
  enabled: true
  client_auth: "verify"
  client_ca_file: /etc/runway/default-ca.pem
```

When set globally, all routes inherit the client mTLS configuration. Per-route settings override global settings.

### Client Auth Modes

| Mode | Cert Required | CA Verification | Use Case |
|------|---------------|-----------------|----------|
| `verify` (default) | Yes | Yes | Production — full chain validation |
| `require` | Yes | No | Cert presence check only (CA pool not needed) |
| `request` | No | No | Optional — pass through if no cert |

### Fields

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | bool | Enable per-route client mTLS |
| `client_auth` | string | `"request"`, `"require"`, or `"verify"` (default `"verify"`) |
| `client_ca_file` | string | Path to a single CA PEM file |
| `client_cas` | []string | Paths to multiple CA PEM files |
| `allow_expired` | bool | Skip certificate expiry check (testing only) |

## HTTP/2 Limitation

HTTP/2 does not support TLS renegotiation. The listener must request client certificates from **all** clients during the initial TLS handshake, even if only some routes require them. Clients connecting to routes without `client_mtls` will be asked for a certificate but won't be rejected for not providing one (assuming the listener uses `client_auth: "request"`).

## Admin Endpoint

```bash
curl http://localhost:8081/client-mtls
```

Response:

```json
{
  "payments": {
    "verified": 1500,
    "rejected": 23
  },
  "internal": {
    "verified": 800,
    "rejected": 2
  }
}
```

## Relationship to Existing mTLS Middleware

The existing `mtls` middleware (`internal/middleware/mtls/`) extracts certificate information into `variables.CertInfo` for use in expressions and logging. It runs in the global handler chain and is unaffected by this feature.

The `client_mtls` middleware runs per-route and only performs verification/rejection. Both can be active simultaneously — the global middleware extracts cert info, and the per-route middleware enforces CA trust.
