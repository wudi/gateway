---
title: "Cluster Mode"
sidebar_position: 6
---

Cluster mode separates the runway into a **control plane** (CP) that owns the configuration and one or more **data planes** (DP) that proxy traffic. The CP pushes config to every DP over a bidirectional gRPC stream secured with mutual TLS. Each DP caches the last good config to disk so it can restart independently if the CP is temporarily unreachable.

---

## Deployment Modes

| Mode | `cluster.role` | Description |
|------|---------------|-------------|
| Standalone | `standalone` (default) | Single-process runway. Config loaded from local YAML file. No cluster coordination. |
| Control Plane | `control_plane` | Runs a gRPC server that pushes config to connected DPs. Also serves traffic if listeners are configured. |
| Data Plane | `data_plane` | Receives config from the CP. Does not read a local config file for routes/middleware. `POST /reload` returns 403. |

---

## Architecture

```
┌───────────────┐         gRPC/mTLS          ┌───────────────┐
│ Control Plane │◄──── bidi stream ──────────►│  Data Plane 1 │
│  (config push │         :9443               │  (proxies     │
│   + optional  │                             │   traffic)    │
│   traffic)    │                             └───────────────┘
│               │         gRPC/mTLS          ┌───────────────┐
│               │◄──── bidi stream ──────────►│  Data Plane 2 │
└───────────────┘                             └───────────────┘
```

1. The DP opens a bidirectional gRPC stream to the CP and sends a `ConnectRequest` containing its node ID, hostname, binary version, and current config hash.
2. The CP verifies version compatibility (major.minor must match) and sends the current config if the DP's hash differs.
3. When the CP config changes (file reload or `POST /api/v1/config`), it pushes the new config YAML to all connected DPs immediately.
4. Each DP sends periodic heartbeats (default every 10s) reporting its config version, hash, and runtime status (routes, healthy routes, active connections, last reload error).
5. The CP marks nodes as `stale` if no heartbeat arrives within 3x the heartbeat interval.

---

## Configuration

### Control Plane

```yaml
cluster:
  role: control_plane
  control_plane:
    address: ":9443"
    tls:
      enabled: true
      cert_file: /etc/runway/certs/cp.crt
      key_file: /etc/runway/certs/cp.key
      client_ca_file: /etc/runway/certs/ca.crt

listeners:
  - id: http
    address: ":8080"
    protocol: http

admin:
  enabled: true
  port: 8081

routes:
  - id: api
    path: /api
    path_prefix: true
    backends:
      - url: http://backend:9000
```

The CP loads routes, middleware, and all other config from its local YAML file (or via `POST /api/v1/config`). It pushes the full config (with the `cluster` block stripped) to every connected DP.

### Data Plane

```yaml
cluster:
  role: data_plane
  data_plane:
    address: "cp.example.com:9443"
    tls:
      enabled: true
      cert_file: /etc/runway/certs/dp.crt
      key_file: /etc/runway/certs/dp.key
      ca_file: /etc/runway/certs/ca.crt
    cache_dir: /var/lib/runway/cluster
    retry_interval: 5s
    heartbeat_interval: 10s
    node_id: ""

listeners:
  - id: http
    address: ":8080"
    protocol: http

admin:
  enabled: true
  port: 8081
```

The DP's YAML only needs `cluster`, `listeners`, and `admin`. Routes and middleware come from the CP. If `node_id` is empty, a UUID is generated and persisted in `cache_dir/node_id` so the node keeps the same identity across restarts.

### Standalone (default)

```yaml
# No cluster block needed, or:
cluster:
  role: standalone
```

Omitting the `cluster` block entirely or setting `role: standalone` runs the runway in single-process mode with no cluster coordination.

---

## TLS / mTLS Requirements

All CP-DP communication uses mutual TLS (TLS 1.3 minimum). Both sides present certificates and verify the peer's certificate against a CA pool.

| Side | Presents | Verifies against |
|------|----------|------------------|
| Control Plane | `control_plane.tls.cert_file` + `key_file` | `control_plane.tls.client_ca_file` (verifies DP client certs) |
| Data Plane | `data_plane.tls.cert_file` + `key_file` | `data_plane.tls.ca_file` (verifies CP server cert) |

**Validation enforces:**

- `control_plane.tls.enabled: true` is required for CP role
- `control_plane.tls.cert_file`, `key_file`, and `client_ca_file` are all required
- `data_plane.tls.enabled: true` is required for DP role
- `data_plane.tls.cert_file`, `key_file`, and `ca_file` are all required

A typical PKI setup uses a single CA that signs both the CP server certificate and all DP client certificates. The CP's `client_ca_file` and each DP's `ca_file` point to the same CA certificate.

---

## Fleet Monitoring

### Admin API

**CP-only endpoints:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/cluster/nodes` | GET | Returns an array of all connected DP nodes with status |
| `/api/v1/config` | GET | Returns the current config YAML |
| `/api/v1/config` | POST | Push a new config YAML (validates, applies locally, then pushes to all DPs) |
| `/api/v1/config/hash` | GET | Returns current config version, hash, timestamp, and source |

**DP-only endpoints:**

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/cluster/status` | GET | Returns this DP's cluster status |

**DP restrictions:**

| Endpoint | Behavior |
|----------|----------|
| `POST /reload` | Returns `403 Forbidden` with message "config changes must go through control plane" |

#### Example: List connected nodes (CP)

```bash
curl http://cp:8081/cluster/nodes
```

```json
[
  {
    "node_id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
    "hostname": "dp-pod-1",
    "version": "1.4.0",
    "config_version": 3,
    "config_hash": 12345678901234567,
    "last_heartbeat": "2026-02-25T10:30:05Z",
    "status": "connected",
    "node_status": {
      "runway_version": "1.4.0",
      "last_reload_error": "",
      "last_successful_version": 3
    }
  },
  {
    "node_id": "b2c3d4e5-6789-01ab-cdef-2345678901bc",
    "hostname": "dp-pod-2",
    "version": "1.4.0",
    "config_version": 3,
    "config_hash": 12345678901234567,
    "last_heartbeat": "2026-02-25T10:30:03Z",
    "status": "connected",
    "node_status": {
      "runway_version": "1.4.0",
      "last_reload_error": "",
      "last_successful_version": 3
    }
  }
]
```

Node `status` is either `"connected"` (heartbeat received within 3x the heartbeat interval) or `"stale"` (no recent heartbeat).

#### Example: Check config version (CP)

```bash
curl http://cp:8081/api/v1/config/hash
```

```json
{
  "version": 3,
  "hash": 12345678901234567,
  "timestamp": "2026-02-25T10:28:00Z",
  "source": "file"
}

```

The `source` field indicates how the config was last updated: `"file"` (config reload from disk), `"admin-api"` (via `POST /api/v1/config`), or `"init"` (initial load at startup).

#### Example: Push new config (CP)

```bash
curl -X POST http://cp:8081/api/v1/config \
  -H "Content-Type: application/yaml" \
  --data-binary @runway.yaml
```

The CP validates the config, applies it locally, and pushes it to all connected DPs. Returns the reload result:

```json
{
  "Success": true,
  "Timestamp": "2026-02-25T10:35:00Z",
  "Changes": ["route:api-v2 added"]
}
```

On validation failure returns `400`; on apply failure returns `422`.

#### Example: Check DP status

```bash
curl http://dp:8081/cluster/status
```

```json
{
  "role": "data_plane",
  "cp_address": "cp.example.com:9443",
  "connected": true,
  "config_version": 3,
  "config_hash": 12345678901234567,
  "node_id": "a1b2c3d4-5678-90ab-cdef-1234567890ab",
  "has_config": true
}
```

---

## Static Stability

When a DP successfully applies a config from the CP, it writes the config YAML to `cache_dir/config.yaml` using atomic write (tmp + fsync + rename). On startup, if the CP is unreachable, the DP loads the cached config and begins serving traffic immediately.

The cached config is validated before use. If validation fails (e.g., after a binary upgrade that changed config schema), the DP starts without a config and waits for the CP.

The node ID is also persisted to `cache_dir/node_id` so the DP keeps the same identity across restarts.

**Default cache directory:** `/var/lib/runway/cluster`

---

## Operational Notes

### One config source per CP

The CP accepts config from its local YAML file (via `POST /reload` or `SIGHUP`) and from the admin API (`POST /api/v1/config`). Both paths feed into the same reload pipeline and push the result to all DPs. Avoid using both sources simultaneously to prevent config thrashing. Pick one source of truth: either file-based with a CI/CD pipeline, or API-based with an external config management system.

### Version skew policy

The CP and DP must share the same **major.minor** version. A DP running `1.4.2` can connect to a CP running `1.4.0`, but a DP running `1.5.0` will be rejected by a CP running `1.4.0` with a `FailedPrecondition` gRPC error:

```
version mismatch: CP=1.4.0 DP=1.5.0 (major.minor must match)
```

Upgrade the CP first, then roll out DP updates.

### Reconnection

When the connection to the CP is lost, the DP reconnects with exponential backoff starting at `retry_interval` (default 5s) up to a maximum of 60s. The DP continues serving traffic using its last known config throughout the disconnection.

### Config overlay

The DP overlays its own `cluster` block onto every config received from the CP. This means the DP's cluster settings (address, TLS paths, cache dir, etc.) are always preserved regardless of what the CP sends.

### Config hash integrity

Each config update includes an xxhash64 checksum. The DP independently computes the hash over the received YAML and rejects the update if the hashes do not match.

---

## Admin API Endpoints by Role

| Endpoint | Standalone | Control Plane | Data Plane |
|----------|-----------|---------------|------------|
| `GET /cluster/nodes` | -- | Yes | -- |
| `GET /api/v1/config` | -- | Yes | -- |
| `POST /api/v1/config` | -- | Yes | -- |
| `GET /api/v1/config/hash` | -- | Yes | -- |
| `GET /cluster/status` | -- | -- | Yes |
| `POST /reload` | Yes | Yes | **403** |
| All other admin endpoints | Yes | Yes | Yes |
