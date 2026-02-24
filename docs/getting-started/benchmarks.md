---
title: "Gateway Comparison Benchmarks"
sidebar_position: 4
---

Compare this gateway's proxy performance against Kong, KrakenD, Traefik, and Tyk using identical backends and load conditions.

## Prerequisites

- Docker and Docker Compose v2
- Sufficient resources (4+ CPU cores, 8+ GB RAM recommended)
- No other services on port 8080

## Quick Start

```bash
# Quick comparison: this gateway vs Kong, 30s
make compare-bench-quick

# Full 5-gateway comparison, simple proxy, 60s
make compare-bench

# All scenarios (simple + auth + ratelimit), all gateways, 60s each
make compare-bench-full

# Tear down all services
make compare-down
```

## Test Scenarios

| Scenario | Path | Features | Gateways |
|----------|------|----------|----------|
| `simple` | `/simple` | Proxy passthrough only | All 5 |
| `auth` | `/auth` | API key validation | gw, Kong, Tyk |
| `ratelimit` | `/ratelimit` | 10k req/s rate limit | All 5 |

KrakenD and Traefik are skipped for the `auth` scenario because they lack native API key authentication comparable to the other gateways.

## Configuration

Environment variables control benchmark parameters:

| Variable | Default | Description |
|----------|---------|-------------|
| `GATEWAYS` | `gw kong krakend traefik tyk` | Space-separated list of gateways to test |
| `SCENARIOS` | `simple` | Space-separated list of scenarios |
| `DURATION` | `60` | Test duration in seconds |
| `WARMUP` | `5` | Warmup duration in seconds |
| `CONCURRENCY` | `50` | Number of concurrent connections |
| `QPS` | `0` | Requests per second limit (0 = unlimited) |

Example:

```bash
GATEWAYS="gw kong" DURATION=120 CONCURRENCY=100 make compare-bench
```

## Output

Results are saved to `perf/compare/results/<timestamp>/` containing:

- `<gateway>-<scenario>-hey.txt` — Raw `hey` output with latency distribution
- `<gateway>-<scenario>-stats.csv` — Docker CPU/memory samples at 1s intervals
- `REPORT.md` — Markdown comparison table

### Report Format

| Gateway | RPS | Avg (ms) | P50 (ms) | P95 (ms) | P99 (ms) | Avg CPU% | Peak Mem (MB) |
|---------|-----|----------|----------|----------|----------|----------|---------------|

## Architecture

Each gateway runs one at a time to avoid resource contention. The flow for each gateway:

1. Start the gateway container (with profile isolation)
2. Wait for health check to pass
3. Run a short warmup with `hey`
4. Collect `docker stats` in the background
5. Run the benchmark with `hey`
6. Save results and stop the gateway

All gateways proxy to the same nginx backend returning a static JSON response.

## Gateway Versions

| Gateway | Image | Notes |
|---------|-------|-------|
| This gateway | Built from source | Dockerfile in project root |
| Kong | `kong:3.9` | DB-less declarative mode |
| KrakenD | `devopsfaith/krakend:2.7` | `no-op` encoding (no transform) |
| Traefik | `traefik:v3.3` | File provider |
| Tyk | `tykio/tyk-gateway:v5.7` | Requires Redis (included) |

## Caveats

- All gateways run as Docker containers with default resource limits on the same host. Results reflect containerized performance, not bare-metal.
- `hey` also runs in Docker on the same host, sharing CPU. Network overhead is minimal (Docker bridge) but CPU is shared between the load generator and gateway.
- Rate limit implementations differ across gateways (token bucket, sliding window, fixed window). The `ratelimit` scenario validates throughput under a high limit, not precision.
- Tyk requires Redis as a dependency. This is standard for Tyk deployments but adds an extra container.
- KrakenD uses `no-op` encoding, which passes responses through without JSON parsing. This is the fairest comparison for pure proxy throughput.
- For production decisions, run benchmarks on representative hardware with your actual workload patterns.
