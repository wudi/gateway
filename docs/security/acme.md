---
title: "ACME / Let's Encrypt"
sidebar_position: 19
---

Automatic certificate management via the ACME protocol. The runway can obtain and renew TLS certificates from Let's Encrypt (or any ACME-compatible CA) without manual intervention.

## How It Works

The [ACME protocol](https://datatracker.ietf.org/doc/html/rfc8555) automates domain validation and certificate issuance. The runway acts as an ACME client: it proves control of the configured domains to the CA, receives a signed certificate, and automatically renews it before expiry. Certificates and account keys are cached on disk so restarts do not trigger re-issuance.

## Challenge Types

ACME requires the CA to verify that you control the domain. The runway supports two challenge types:

### TLS-ALPN-01 (default)

The CA connects to port 443 and performs a TLS handshake with a special ALPN protocol identifier. The runway responds with a self-signed validation certificate that proves domain control.

- **No port 80 required** -- validation happens over the same TLS listener.
- Best for deployments where only port 443 is exposed.
- The listener must be reachable on port 443 from the internet (or wherever the CA connects from).

### HTTP-01

The CA makes an HTTP request to `http://<domain>/.well-known/acme-challenge/<token>`. The runway starts a temporary HTTP server to respond.

- Requires **port 80** to be available and reachable from the internet.
- Useful when port 443 is behind a load balancer that terminates TLS, or when the listener runs on a non-standard port.

## Configuration

ACME is configured under the `tls` block of a listener. It is **mutually exclusive** with manual `cert_file`/`key_file` -- you use one or the other, not both.

### Basic ACME (TLS-ALPN-01)

```yaml
listeners:
  - id: "https"
    address: ":443"
    protocol: "http"
    tls:
      enabled: true
      acme:
        enabled: true
        domains:
          - "example.com"
          - "www.example.com"
        email: "admin@example.com"
```

This uses the default TLS-ALPN-01 challenge. The runway handles validation on the same TLS port.

### HTTP-01 Challenge

```yaml
listeners:
  - id: "https"
    address: ":443"
    protocol: "http"
    tls:
      enabled: true
      acme:
        enabled: true
        domains:
          - "example.com"
        email: "admin@example.com"
        challenge_type: "http-01"
        http_address: ":80"
```

The runway starts a temporary HTTP listener on `http_address` (default `:80`) to serve ACME challenge responses.

### ACME with HTTP/3

```yaml
listeners:
  - id: "https"
    address: ":443"
    protocol: "http"
    tls:
      enabled: true
      acme:
        enabled: true
        domains:
          - "example.com"
        email: "admin@example.com"
    http:
      enable_http3: true
```

Once the ACME certificate is obtained, HTTP/3 (QUIC) uses the same certificate automatically.

### Staging Directory

Use the Let's Encrypt staging environment for testing to avoid rate limits:

```yaml
listeners:
  - id: "https"
    address: ":443"
    protocol: "http"
    tls:
      enabled: true
      acme:
        enabled: true
        domains:
          - "example.com"
        email: "admin@example.com"
        directory_url: "https://acme-staging-v02.api.letsencrypt.org/directory"
```

The staging CA issues certificates signed by a test root that browsers do not trust. This is useful for verifying your ACME setup before switching to production.

### All Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable ACME certificate management |
| `domains` | [string] | -- | Domain names to obtain certificates for (required when enabled) |
| `email` | string | -- | Contact email for the ACME account (required when enabled) |
| `directory_url` | string | Let's Encrypt production | ACME directory URL; override for staging or alternative CAs |
| `cache_dir` | string | `/var/lib/runway/acme` | Directory to store certificates and account keys |
| `challenge_type` | string | `tls-alpn-01` | Challenge type: `tls-alpn-01` or `http-01` |
| `http_address` | string | `:80` | Bind address for the HTTP-01 challenge server (only used when `challenge_type` is `http-01`) |

## Cache Directory

The `cache_dir` stores:

- The ACME account private key
- Issued certificates and their private keys
- Renewal metadata

The runway process must have **read and write** permissions on this directory. On first run, the directory is created automatically if it does not exist.

```bash
# Recommended permissions
mkdir -p /var/lib/runway/acme
chmod 700 /var/lib/runway/acme
chown runway:runway /var/lib/runway/acme
```

In container deployments, mount a persistent volume at the cache directory so certificates survive container restarts:

```yaml
volumes:
  - acme-data:/var/lib/runway/acme
```

## Monitoring

### Certificate Status

Query the admin API for certificate details:

```bash
curl http://localhost:8081/certificates
```

```json
{
  "listeners": {
    "https": {
      "mode": "acme",
      "domains": ["example.com", "www.example.com"],
      "not_before": "2026-02-01T00:00:00Z",
      "not_after": "2026-05-01T00:00:00Z",
      "days_remaining": 65,
      "issuer": "Let's Encrypt"
    }
  }
}
```

For listeners using manual certificates, `mode` is `"manual"` and `domains` reflects the certificate's Subject Alternative Names.

### Health Check

The `/health` endpoint includes a `tls_certificates` check:

```json
{
  "status": "ok",
  "checks": {
    "tls_certificates": {
      "status": "ok",
      "listeners": {
        "https": {
          "days_remaining": 65
        }
      }
    }
  }
}
```

The check reports `"ok"` when all TLS certificates have more than 7 days until expiry. It reports `"degraded"` when any certificate has 7 or fewer days remaining, which may indicate a renewal failure.

## Mutual Exclusion with Manual Certificates

A listener's TLS block uses **either** ACME or manual certificate files -- never both. If `acme.enabled` is `true`, the `cert_file` and `key_file` fields must not be set. Config validation rejects configurations that specify both.

```yaml
# Valid: ACME only
tls:
  enabled: true
  acme:
    enabled: true
    domains: ["example.com"]
    email: "admin@example.com"

# Valid: manual only
tls:
  enabled: true
  cert_file: "/etc/tls/cert.pem"
  key_file: "/etc/tls/key.pem"

# Invalid: both ACME and manual -- rejected at startup
tls:
  enabled: true
  cert_file: "/etc/tls/cert.pem"
  key_file: "/etc/tls/key.pem"
  acme:
    enabled: true
    domains: ["example.com"]
    email: "admin@example.com"
```

## Renewal

Certificates are automatically renewed before expiry. The ACME client library handles renewal scheduling internally. No configuration is needed -- the runway checks certificate validity on startup and periodically, and renews when the certificate approaches expiry (typically at the two-thirds mark of its lifetime).

If renewal fails, the runway continues serving the existing certificate and retries renewal. The `/health` endpoint's `tls_certificates` check transitions to `"degraded"` when a certificate has 7 or fewer days remaining, providing an early warning to investigate renewal failures.
