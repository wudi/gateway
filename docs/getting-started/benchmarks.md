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
# Quick comparison: this runway vs Kong, 30s
make compare-bench-quick

# Full 5-proxy comparison, simple proxy, 60s
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

## Results

Tested on Linux 5.10, 4 CPUs, 8 GB RAM, Docker 29.1, 50 concurrent connections, 60s duration per test.

### Simple Proxy

| Gateway | RPS | Avg (ms) | P50 (ms) | P95 (ms) | P99 (ms) | Avg CPU% | Peak Mem (MB) |
|---------|-----|----------|----------|----------|----------|----------|---------------|
| **Runway (this)** | **15688** | **3.2** | **2.6** | **7.5** | **11.7** | 155.9 | **44.5** |
| Kong 3.9 | 14750 | 3.4 | 2.7 | 8.4 | 12.6 | 120.9 | 572.3 |
| KrakenD 2.7 | 9405 | 5.3 | 4.2 | 13.9 | 21.0 | 155.6 | 42.6 |
| Traefik 3.3 | 14783 | 3.4 | 2.8 | 8.0 | 12.5 | 158.3 | 59.0 |
| Tyk 5.7 | 1532 | 32.6 | 9.4 | 162.5 | 435.8 | 342.5 | 57.9 |

### API Key Authentication

| Gateway | RPS | Avg (ms) | P50 (ms) | P95 (ms) | P99 (ms) | Avg CPU% | Peak Mem (MB) |
|---------|-----|----------|----------|----------|----------|----------|---------------|
| **Runway (this)** | **15038** | **3.3** | 2.8 | **7.9** | **12.1** | 156.0 | **47.3** |
| Kong 3.9 | 12140 | 4.1 | **1.9** | 13.1 | 19.9 | 123.0 | 551.2 |
| Tyk 5.7 | 9345 | 5.3 | 3.9 | 14.9 | 20.8 | 180.4 | 58.6 |

KrakenD and Traefik are excluded — they lack native API key authentication.

### Rate Limiting (10k req/s)

| Gateway | RPS | Avg (ms) | P50 (ms) | P95 (ms) | P99 (ms) | Avg CPU% | Peak Mem (MB) |
|---------|-----|----------|----------|----------|----------|----------|---------------|
| **Runway (this)** | **13946** | 3.6 | 3.0 | 8.4 | 12.9 | 158.0 | **44.9** |
| Kong 3.9 | 7231 | 6.9 | 6.2 | 14.6 | 21.9 | 170.6 | 552.1 |
| KrakenD 2.7 | 10010 | 5.0 | 3.9 | 13.1 | 20.0 | 154.5 | 49.4 |
| Traefik 3.3 | 18939 | 3.0 | 2.1 | 7.0 | 11.5 | 157.7 | 62.4 |
| Tyk 5.7 | 52091 | 3.0 | 0.8 | 2.0 | 3.5 | 153.9 | 35.2 |

Note: RPS counts all responses including rejections. Traefik's high total RPS includes 429 rejections (~43% of responses); its successful throughput is ~9.5k req/s, correctly enforcing the limit. Tyk returned 404 for all requests in this scenario (route misconfiguration in the benchmark), so its numbers are not meaningful.

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
