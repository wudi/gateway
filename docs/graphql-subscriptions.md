# GraphQL Subscriptions

The GraphQL subscription middleware enables WebSocket-based GraphQL subscriptions through the gateway. It detects and handles the GraphQL-over-WebSocket protocols, managing connection lifecycle and enforcing connection limits.

## Configuration

Nested within the per-route `graphql` block:

```yaml
routes:
  - id: "graphql-api"
    path: "/graphql"
    backends:
      - url: "http://graphql-backend:4000"
    graphql:
      enabled: true
      max_depth: 10
      subscriptions:
        enabled: true
        protocol: "graphql-transport-ws"
        ping_interval: 30s
        max_connections: 100
```

## Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `graphql.subscriptions.enabled` | bool | false | Enable subscription support |
| `graphql.subscriptions.protocol` | string | graphql-transport-ws | WebSocket sub-protocol |
| `graphql.subscriptions.ping_interval` | duration | 30s | Keepalive ping interval |
| `graphql.subscriptions.max_connections` | int | 0 | Max concurrent subscription connections (0 = unlimited) |

## Supported Protocols

- **`graphql-transport-ws`** (default) -- The newer protocol (`graphql-ws` npm package). Uses `connection_init`, `subscribe`, `next`, `complete` messages.
- **`graphql-ws`** -- The legacy protocol (`subscriptions-transport-ws` npm package). Uses `connection_init`, `start`, `data`, `stop` messages.

The protocol is negotiated via the `Sec-WebSocket-Protocol` header during the WebSocket upgrade.

## Connection Limiting

When `max_connections` is set, the middleware limits concurrent WebSocket subscription connections per route. New connections beyond the limit are rejected with `503 Service Unavailable`.

## Integration

GraphQL subscriptions work with the existing WebSocket proxy layer. The subscription middleware handles protocol detection and connection management, then delegates the actual WebSocket proxying to the gateway's WebSocket handler.

## Admin Endpoint

`GET /graphql-subscriptions` returns per-route subscription connection statistics.

```bash
curl http://localhost:8081/graphql-subscriptions
```

See [Configuration Reference](configuration-reference.md#graphql) for the full GraphQL configuration block.
