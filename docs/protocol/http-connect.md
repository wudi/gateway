---
title: "HTTP CONNECT Tunneling"
sidebar_position: 9
---

The CONNECT middleware enables the runway to act as an HTTP CONNECT proxy, establishing TCP tunnels to allowed destinations. This is useful for proxying TLS connections, database traffic, or other TCP-based protocols through the runway.

## Configuration

Per-route:

```yaml
routes:
  - id: "tunnel"
    path: "/connect"
    backends:
      - url: "http://placeholder:9000"
    connect:
      enabled: true
      allowed_hosts:
        - "*.internal.example.com"
        - "db.example.com"
      allowed_ports:
        - 443
        - 5432
        - 3306
      connect_timeout: 10s
      idle_timeout: 5m
      max_tunnels: 100
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `connect.enabled` | bool | false | Enable CONNECT tunneling |
| `connect.allowed_hosts` | []string | -- | Host glob patterns allowed for tunneling |
| `connect.allowed_ports` | []int | -- | Ports allowed for tunneling |
| `connect.connect_timeout` | duration | 10s | Timeout for establishing the tunnel connection |
| `connect.idle_timeout` | duration | 5m | Close tunnel after this idle duration |
| `connect.max_tunnels` | int | 0 | Max concurrent tunnels (0 = unlimited) |

## Host Matching

The `allowed_hosts` field supports glob patterns:
- `"db.example.com"` -- exact match
- `"*.internal.example.com"` -- wildcard subdomain match
- `"10.0.*.*"` -- wildcard IP match

If a client requests a CONNECT to a host or port not in the allow lists, the request is rejected with `403 Forbidden`.

## Tunnel Lifecycle

1. The client sends an HTTP CONNECT request with the target `host:port`.
2. The middleware validates the host and port against the allow lists.
3. A TCP connection is established to the target within `connect_timeout`.
4. The runway responds with `200 Connection Established`.
5. Data is bidirectionally copied between client and target.
6. The tunnel closes when either side disconnects or `idle_timeout` is reached.

## Admin Endpoint

`GET /connect` returns per-route tunnel statistics.

```bash
curl http://localhost:8081/connect
```

See [Configuration Reference](../reference/configuration-reference.md#connect-per-route) for field details.
