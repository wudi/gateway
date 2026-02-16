#!/usr/bin/env bash
set -euo pipefail

# Gateway benchmark comparison orchestrator.
# Runs hey against each gateway one at a time and produces a markdown report.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPARE_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_ROOT="$(cd "$COMPARE_DIR/../.." && pwd)"

# Configurable via environment
GATEWAYS="${GATEWAYS:-gw kong krakend traefik tyk}"
SCENARIOS="${SCENARIOS:-simple}"
DURATION="${DURATION:-60}"
WARMUP="${WARMUP:-5}"
CONCURRENCY="${CONCURRENCY:-50}"
QPS="${QPS:-0}"  # 0 = unlimited

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
RESULTS_DIR="$COMPARE_DIR/results/$TIMESTAMP"
mkdir -p "$RESULTS_DIR"

COMPOSE="docker compose -f $COMPARE_DIR/docker-compose.compare.yaml"

echo "=== Gateway Benchmark Comparison ==="
echo "Gateways:    $GATEWAYS"
echo "Scenarios:   $SCENARIOS"
echo "Duration:    ${DURATION}s"
echo "Warmup:      ${WARMUP}s"
echo "Concurrency: $CONCURRENCY"
echo "QPS limit:   ${QPS:-unlimited}"
echo "Results:     $RESULTS_DIR"
echo ""

# Ensure backend is running
echo "--- Starting shared backend ---"
$COMPOSE up -d compare-backend
$COMPOSE up -d --wait compare-backend

# Start hey container
$COMPOSE --profile bench up -d hey

for scenario in $SCENARIOS; do
  echo ""
  echo "=== Scenario: $scenario ==="
  for gw in $GATEWAYS; do
    echo ""
    echo "--- Benchmarking: $gw ($scenario) ---"
    "$SCRIPT_DIR/bench-one.sh" \
      "$gw" "$scenario" "$RESULTS_DIR" \
      "$DURATION" "$WARMUP" "$CONCURRENCY" "$QPS"
  done
done

# Stop hey and backend
$COMPOSE --profile bench --profile gw --profile kong --profile krakend --profile traefik --profile tyk stop 2>/dev/null || true
$COMPOSE --profile bench --profile gw --profile kong --profile krakend --profile traefik --profile tyk rm -f 2>/dev/null || true
$COMPOSE down 2>/dev/null || true

# Generate report
echo ""
echo "=== Generating report ==="
"$SCRIPT_DIR/report.sh" "$RESULTS_DIR" "$SCENARIOS"

echo ""
echo "Results saved to: $RESULTS_DIR"
echo "Report: $RESULTS_DIR/REPORT.md"
