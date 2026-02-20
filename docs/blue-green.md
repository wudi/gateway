# Blue-Green Deployment Controller

The blue-green deployment controller provides first-class support for binary all-or-nothing traffic cutover between two backend groups. Unlike canary deployments which progressively shift traffic weights, blue-green performs an atomic switch: 100% of traffic moves from the active group to the inactive group in a single operation, with an observation window for automatic rollback if error thresholds are breached.

## How It Works

1. Configure a route with `traffic_split` containing two groups (e.g., "blue" and "green")
2. Enable `blue_green` and designate one group as the initial `active_group`
3. The gateway routes 100% of traffic to the active group
4. To deploy a new version, trigger a promotion via the admin API
5. During promotion, the controller enters an observation window where it monitors error rate
6. If the error rate exceeds the threshold during observation, the controller automatically rolls back
7. If the observation window completes successfully, the new group becomes permanently active

## Configuration

Blue-green is per-route only.

```yaml
routes:
  - id: api
    path: /api
    path_prefix: true
    traffic_split:
      - name: blue
        weight: 100
        backends:
          - url: http://api-blue-1:8080
          - url: http://api-blue-2:8080
      - name: green
        weight: 0
        backends:
          - url: http://api-green-1:8080
          - url: http://api-green-2:8080
    blue_green:
      enabled: true
      active_group: blue                 # currently receiving traffic
      inactive_group: green              # standby group
      observation:
        window: 5m                       # monitoring period after promotion
        error_threshold: 0.05            # auto-rollback if error rate > 5%
        min_requests: 50                 # minimum requests before evaluating
        interval: 10s                    # evaluation frequency during window
```

### Full Example with Multiple Routes

```yaml
routes:
  - id: web-frontend
    path: /
    path_prefix: true
    traffic_split:
      - name: blue
        weight: 100
        backends:
          - url: http://web-blue:3000
      - name: green
        weight: 0
        backends:
          - url: http://web-green:3000
    blue_green:
      enabled: true
      active_group: blue
      inactive_group: green
      observation:
        window: 10m
        error_threshold: 0.02
        min_requests: 200
        interval: 15s

  - id: api-backend
    path: /api
    path_prefix: true
    traffic_split:
      - name: blue
        weight: 100
        backends:
          - url: http://api-blue-1:8080
          - url: http://api-blue-2:8080
      - name: green
        weight: 0
        backends:
          - url: http://api-green-1:8080
          - url: http://api-green-2:8080
    blue_green:
      enabled: true
      active_group: blue
      inactive_group: green
      observation:
        window: 5m
        error_threshold: 0.05
        min_requests: 100
        interval: 10s
```

### Configuration Fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable blue-green deployment for this route |
| `active_group` | string | - | Name of the traffic split group currently receiving traffic |
| `inactive_group` | string | - | Name of the standby traffic split group |
| `observation.window` | duration | `5m` | Duration to monitor after promotion |
| `observation.error_threshold` | float | `0.05` | Max error rate (0.0-1.0) before auto-rollback |
| `observation.min_requests` | int | `50` | Minimum requests before evaluation begins |
| `observation.interval` | duration | `10s` | How often to check error rate during observation |

## State Machine

```
                    POST /promote
     inactive ─────────────────────> promoting
                                      |      |
                    (observation       |      | (error_threshold
                     window done,      |      |  exceeded)
                     healthy)          |      |
                                      v      v
                                   active   rolled_back
```

### States

| State | Description |
|-------|-------------|
| `inactive` | Initial state. The configured `active_group` receives 100% traffic. No promotion in progress. |
| `promoting` | Traffic has been switched to the previously inactive group. Observation window is active, monitoring error rate. |
| `active` | Observation window completed successfully. The new group is now permanently active. |
| `rolled_back` | Error threshold was exceeded during observation. Traffic was automatically switched back to the original group. |

After a `rolled_back` or `active` state, the controller resets to `inactive` ready for the next deployment cycle when a new promotion is triggered.

## Promotion Flow

1. **Trigger promotion**: `POST /blue-green/{route}/promote`
2. The controller atomically sets the inactive group's weight to 100 and the active group's weight to 0
3. State transitions to `promoting`
4. The observation timer starts; error rate is evaluated every `observation.interval`
5. If fewer than `min_requests` have been received, evaluation is skipped for that interval
6. If the error rate exceeds `error_threshold`, the controller:
   - Switches weights back (original group to 100, promoted group to 0)
   - Sets state to `rolled_back`
   - Logs the rollback reason and error rate
7. If the observation window completes without threshold breach:
   - State transitions to `active`
   - The promoted group remains at 100% permanently

## Rollback

### Automatic Rollback

During the `promoting` state, if the error rate of the newly active group exceeds `error_threshold`, traffic is automatically switched back. The `rolled_back` state includes the error rate at the time of rollback.

### Manual Rollback

Force an immediate rollback at any time during promotion:

```bash
curl -X POST http://localhost:8081/blue-green/api/rollback
```

Manual rollback switches traffic back to the original group and sets state to `rolled_back` regardless of current error rate.

## Mutual Exclusivity with Canary

Blue-green and canary deployments are mutually exclusive on the same route. A route cannot have both `blue_green.enabled: true` and `canary.enabled: true` -- this is a config validation error. Blue-green performs binary cutover while canary performs gradual weight shifting; combining them would create conflicting weight management.

## Validation Rules

- `blue_green` requires `traffic_split` to be configured on the route
- `active_group` must reference an existing traffic split group name
- `inactive_group` must reference an existing traffic split group name
- `active_group` and `inactive_group` must be different
- `traffic_split` must contain exactly the two groups referenced by `active_group` and `inactive_group`
- `observation.error_threshold` must be between 0.0 and 1.0
- `observation.window` must be > 0
- `observation.interval` must be > 0
- `observation.min_requests` must be >= 0
- Blue-green and canary are mutually exclusive on the same route

## Admin API

### GET `/blue-green`

Returns blue-green deployment status for all routes.

```bash
curl http://localhost:8081/blue-green
```

**Response:**
```json
{
  "api": {
    "state": "inactive",
    "active_group": "blue",
    "inactive_group": "green",
    "observation_window": "5m0s",
    "error_threshold": 0.05
  },
  "web-frontend": {
    "state": "promoting",
    "active_group": "green",
    "inactive_group": "blue",
    "observation_window": "10m0s",
    "error_threshold": 0.02,
    "observation_started": "2026-02-20T14:30:00Z",
    "observation_remaining": "7m23s",
    "current_error_rate": 0.01,
    "requests_in_window": 1250
  }
}
```

### GET `/blue-green/{route}/status`

Returns detailed blue-green status for a specific route.

```bash
curl http://localhost:8081/blue-green/api/status
```

**Response:**
```json
{
  "state": "inactive",
  "active_group": "blue",
  "inactive_group": "green",
  "observation": {
    "window": "5m0s",
    "error_threshold": 0.05,
    "min_requests": 50,
    "interval": "10s"
  },
  "last_promotion": {
    "timestamp": "2026-02-20T10:15:00Z",
    "from_group": "green",
    "to_group": "blue",
    "result": "active",
    "duration": "5m0s"
  }
}
```

### POST `/blue-green/{route}/promote`

Initiates a promotion, switching traffic from the active group to the inactive group.

```bash
curl -X POST http://localhost:8081/blue-green/api/promote
```

**Responses:**

| Status | Condition |
|--------|-----------|
| 200 | Promotion started successfully |
| 409 | A promotion is already in progress (`promoting` state) |
| 404 | Route not found or blue-green not configured |

**Success response:**
```json
{
  "state": "promoting",
  "from_group": "blue",
  "to_group": "green",
  "observation_window": "5m0s"
}
```

### POST `/blue-green/{route}/rollback`

Forces an immediate rollback to the original group.

```bash
curl -X POST http://localhost:8081/blue-green/api/rollback
```

**Responses:**

| Status | Condition |
|--------|-----------|
| 200 | Rollback completed |
| 409 | No promotion in progress (not in `promoting` state) |
| 404 | Route not found or blue-green not configured |

**Success response:**
```json
{
  "state": "rolled_back",
  "active_group": "blue",
  "reason": "manual rollback"
}
```

## Notes

- Weight changes are applied atomically to the `WeightedBalancer` instance. In-flight requests to the previous group complete normally; only new requests are affected by the switch.
- The observation window starts from the moment of promotion. If the gateway restarts during a promotion, the state resets to `inactive` with the currently configured weights.
- Error rate is calculated as the ratio of 5xx responses to total responses from the newly promoted group during the observation window.
- Blue-green works with any load balancing algorithm configured on the traffic split groups (round-robin, least connections, etc.).
- For progressive traffic shifting with multiple weight steps, see [Canary Deployments](canary-deployments.md).

See [Traffic Management](traffic-management.md) for traffic splitting and sticky sessions.
See [Configuration Reference](configuration-reference.md) for all fields.
