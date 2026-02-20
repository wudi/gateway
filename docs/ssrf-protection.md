# SSRF Protection

Server-Side Request Forgery (SSRF) protection prevents the gateway from making outbound connections to private/internal IP addresses when proxying requests or delivering webhooks. It operates at the transport layer by wrapping the dialer, so all DNS-resolved addresses are validated before any TCP connection is established.

## Configuration

SSRF protection is configured globally (not per-route) since it protects all outbound connections.

```yaml
ssrf_protection:
  enabled: true
  allow_cidrs:                   # exempt specific private CIDRs
    - "10.0.1.0/24"
    - "172.16.5.0/24"
  block_link_local: true         # default true — block 169.254.0.0/16 and fe80::/10
```

## How It Works

1. When a backend request is made, the SSRF-safe dialer resolves the hostname to IP addresses using the system DNS resolver
2. **All** resolved IPs are checked against the blocked ranges before any connection is made
3. If any resolved IP falls in a blocked range (and is not in `allow_cidrs`), the connection is refused
4. The dialer connects directly to the resolved IP to prevent DNS rebinding attacks (where a second DNS lookup could return a different, private IP)

## Blocked Ranges

The following ranges are blocked by default:

| Range | Description |
|-------|-------------|
| `127.0.0.0/8` | Loopback |
| `10.0.0.0/8` | Private (Class A) |
| `172.16.0.0/12` | Private (Class B) |
| `192.168.0.0/16` | Private (Class C) |
| `169.254.0.0/16` | Link-local (when `block_link_local: true`) |
| `0.0.0.0/8` | Unspecified |
| `::1/128` | IPv6 loopback |
| `fc00::/7` | IPv6 unique local |
| `fe80::/10` | IPv6 link-local (when `block_link_local: true`) |

## Allow List

Use `allow_cidrs` to exempt specific private ranges that host legitimate backend services:

```yaml
ssrf_protection:
  enabled: true
  allow_cidrs:
    - "10.0.1.0/24"    # internal API cluster
    - "172.16.5.10/32"  # specific internal service
```

Allowed CIDRs are checked before blocked ranges — if an IP matches an allow entry, it is permitted regardless of blocked ranges.

## Middleware Position

SSRF protection is not a middleware — it operates at the transport layer inside the HTTP dialer. It applies to all proxy requests and webhook deliveries automatically when enabled.

## Admin API

### GET `/ssrf-protection`

Returns the current SSRF protection status.

```bash
curl http://localhost:8081/ssrf-protection
```

**Response (200 OK):**

```json
{
  "enabled": true,
  "blocked_ranges": 9,
  "allowed_ranges": 2,
  "blocked_requests": 15
}
```

When disabled:

```json
{
  "enabled": false
}
```

## Validation

- `allow_cidrs` entries must be valid CIDR notation
- `block_link_local` defaults to `true` when not specified
