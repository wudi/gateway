# Graceful Shutdown & Connection Draining

The gateway supports configurable graceful shutdown with an optional drain delay for zero-downtime deployments behind load balancers and Kubernetes.

## Configuration

```yaml
shutdown:
  timeout: 30s       # total shutdown timeout (default 30s)
  drain_delay: 5s    # delay before stopping listeners (default 0s)
```

### Shutdown Fields

- **`timeout`** — Maximum time for the entire shutdown sequence. Includes the drain delay, in-flight request completion, and resource cleanup. Default `30s`.
- **`drain_delay`** — Time to wait after marking the server as "draining" but before closing listeners. During this delay, readiness checks (`/ready`, `/readyz`) return `503`, signaling load balancers and Kubernetes to stop routing new traffic to this instance. Existing connections continue to be served. Default `0s` (no delay).

## Shutdown Sequence

When the gateway receives `SIGINT` or `SIGTERM`:

1. **Mark draining** — The server sets its drain state. Readiness probes immediately return `503 Service Unavailable`.
2. **Drain delay** — If `drain_delay` is configured, the server waits for the specified duration. This gives external load balancers and Kubernetes time to remove the instance from service endpoints.
3. **Stop admin server** — The admin API server shuts down gracefully.
4. **Stop listeners** — All HTTP/TCP/UDP listeners stop accepting new connections and wait for in-flight requests to complete.
5. **Close L4 proxies** — TCP and UDP proxies are closed.
6. **Close gateway** — Service discovery watchers, health checkers, tracer, Redis client, and webhook dispatcher are shut down.
7. **Done** — Shutdown complete.

If the total `timeout` expires before all steps complete, the server forcefully terminates remaining connections.

## Kubernetes Integration

For zero-downtime deployments in Kubernetes, configure the drain delay to align with the pod termination lifecycle:

```yaml
shutdown:
  timeout: 60s
  drain_delay: 10s
```

Kubernetes sends `SIGTERM` and simultaneously begins removing the pod from Service endpoints. The `drain_delay` ensures the gateway keeps serving requests during the brief window before endpoint removal propagates to all kube-proxies and load balancers.

A typical Kubernetes deployment:

```yaml
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      terminationGracePeriodSeconds: 65  # > shutdown.timeout
      containers:
        - name: gateway
          livenessProbe:
            httpGet:
              path: /health
              port: 8081
          readinessProbe:
            httpGet:
              path: /ready
              port: 8081
            periodSeconds: 5
```

## Manual Drain via Admin API

You can trigger drain mode at runtime without sending a signal. This is useful for blue-green deployments or planned maintenance:

```bash
# Initiate drain — readiness checks will return 503
curl -X POST http://localhost:8081/drain

# Check drain status
curl http://localhost:8081/drain
```

### POST `/drain`

Activates drain mode. Readiness probes immediately begin returning `503`. The server continues to serve in-flight and new requests (listeners remain open), but load balancers observing the readiness probe will stop sending new traffic.

**Response:**
```json
{"status": "draining", "message": "drain mode activated, readiness checks will return 503"}
```

If already draining:
```json
{"status": "already_draining", "message": "server is already in drain mode"}
```

### GET `/drain`

Returns current drain status.

**Response (not draining):**
```json
{"draining": false}
```

**Response (draining):**
```json
{
  "draining": true,
  "drain_start": "2026-01-15T10:30:00Z",
  "drain_duration": "5m30s"
}
```

## Validation

- `timeout` must be >= 0
- `drain_delay` must be >= 0
- `drain_delay` must be less than `timeout` when both are set

## Key Config Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `shutdown.timeout` | duration | 30s | Total graceful shutdown timeout |
| `shutdown.drain_delay` | duration | 0s | Delay before stopping listeners for LB deregistration |

See [Configuration Reference](configuration-reference.md) for all fields.
