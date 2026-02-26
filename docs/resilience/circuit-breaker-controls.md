---
title: "Circuit Breaker Controls"
sidebar_position: 2
---

The admin API provides runtime override endpoints to force circuit breaker state for individual routes. This is useful for operational scenarios like manual failover, testing, or recovering from stuck breakers.

## Override Endpoints

### POST `/circuit-breakers/{route}/open`

Forces the circuit breaker for the specified route into the open state. All requests to the route will be rejected with 503 until the override is cleared.

```bash
curl -X POST http://localhost:8081/circuit-breakers/api/open
```

**Response (200 OK):**
```json
{"status": "ok", "action": "open", "route": "api"}
```

### POST `/circuit-breakers/{route}/close`

Forces the circuit breaker for the specified route into the closed state. Requests will flow to backends regardless of failure counts.

```bash
curl -X POST http://localhost:8081/circuit-breakers/api/close
```

**Response (200 OK):**
```json
{"status": "ok", "action": "close", "route": "api"}
```

### POST `/circuit-breakers/{route}/reset`

Removes the override and returns the circuit breaker to automatic state management. The breaker resumes normal open/closed/half-open transitions based on failure counts and timeouts.

```bash
curl -X POST http://localhost:8081/circuit-breakers/api/reset
```

**Response (200 OK):**
```json
{"status": "ok", "action": "reset", "route": "api"}
```

### Errors

Returns `404` if the route does not have a circuit breaker configured:
```json
{"error": "no circuit breaker for route \"unknown\""}
```

Returns `400` for unknown actions (valid actions: `open`, `close`, `reset`).

Returns `405 Method Not Allowed` for non-POST requests.

## Viewing Override State

The override state is visible in the `GET /circuit-breakers` response as the `override` field:

```bash
curl http://localhost:8081/circuit-breakers
```

```json
{
  "api": {
    "state": "open",
    "override": "open",
    "mode": "local"
  }
}
```

When no override is active, the `override` field is absent or empty.

## Notes

- Overrides are runtime-only and do not persist across gateway restarts or config reloads.
- No configuration changes are needed to use these endpoints -- they work on any route with `circuit_breaker.enabled: true`.
- Overrides work with both `local` and `distributed` circuit breaker modes.

See [Resilience](resilience.md#circuit-breaker) for circuit breaker configuration.
