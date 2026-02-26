#!/usr/bin/env bash
set -euo pipefail

# Benchmark a single proxy for a single scenario.
# Usage: bench-one.sh <gateway> <scenario> <results_dir> <duration> <warmup> <concurrency> <qps>

GATEWAY="$1"
SCENARIO="$2"
RESULTS_DIR="$3"
DURATION="${4:-60}"
WARMUP="${5:-5}"
CONCURRENCY="${6:-50}"
QPS="${7:-0}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPARE_DIR="$(dirname "$SCRIPT_DIR")"
COMPOSE="docker compose -f $COMPARE_DIR/docker-compose.compare.yaml"

# Scenario → URL path and extra headers
declare -A PATHS=( [simple]="/simple" [auth]="/auth" [ratelimit]="/ratelimit" )
declare -A HEADERS=( [simple]="" [auth]="-H X-API-Key:bench-test-key" [ratelimit]="" )

# Skip unsupported combinations
if [[ "$SCENARIO" == "auth" && ("$GATEWAY" == "krakend" || "$GATEWAY" == "traefik") ]]; then
  echo "  SKIP: $GATEWAY does not support native API key auth"
  return 0 2>/dev/null || exit 0
fi

PATH_SUFFIX="${PATHS[$SCENARIO]}"
EXTRA_HEADERS="${HEADERS[$SCENARIO]}"
TARGET="http://${GATEWAY}:8080${PATH_SUFFIX}"

# For Tyk, the service name is 'tyk' but we also need tyk-redis
PROFILE="$GATEWAY"

# Map profile to service names for targeted stop/rm
gateway_services() {
  case "$1" in
    tyk) echo "tyk tyk-redis" ;;
    *)   echo "$1" ;;
  esac
}

cleanup_gateway() {
  echo "  Stopping $GATEWAY..."
  for svc in $(gateway_services "$GATEWAY"); do
    $COMPOSE stop "$svc" 2>/dev/null || true
    $COMPOSE rm -f "$svc" 2>/dev/null || true
  done
  sleep 2
}

# Start the gateway
echo "  Starting $GATEWAY..."
$COMPOSE --profile "$PROFILE" up -d --build 2>/dev/null

# Wait for readiness: use --wait for gateways with healthchecks, manual poll for others
if [[ "$GATEWAY" == "tyk" ]]; then
  # Tyk image is distroless (no curl/wget), poll from hey container
  echo "  Waiting for $GATEWAY to become ready..."
  for i in $(seq 1 30); do
    if $COMPOSE --profile bench exec -T hey /hey -n 1 -c 1 "http://${GATEWAY}:8080/hello" 2>&1 | grep -q '200'; then
      echo "  $GATEWAY is ready"
      break
    fi
    if [[ $i -eq 30 ]]; then
      echo "  ERROR: $GATEWAY failed to become healthy within timeout"
      cleanup_gateway
      exit 1
    fi
    sleep 2
  done
else
  $COMPOSE --profile "$PROFILE" up -d --wait 2>/dev/null || {
    echo "  ERROR: $GATEWAY failed to become healthy within timeout"
    cleanup_gateway
    exit 1
  }
fi

# For Tyk auth scenario, provision an API key
if [[ "$GATEWAY" == "tyk" && "$SCENARIO" == "auth" ]]; then
  echo "  Provisioning Tyk API key..."
  sleep 3  # Let Tyk fully initialize
  $COMPOSE --profile tyk exec -T tyk sh -c '
    wget -q -O /dev/null --method=POST \
      --header="x-tyk-authorization: tyk-bench-secret" \
      --header="Content-Type: application/json" \
      --body-data="{\"alias\":\"bench-client\",\"expires\":-1,\"access_rights\":{\"auth-proxy\":{\"api_id\":\"auth-proxy\",\"api_name\":\"Auth Proxy\",\"versions\":[\"Default\"]}}}" \
      "http://localhost:8080/tyk/keys/bench-test-key"
  ' 2>/dev/null || echo "  Warning: Tyk key provisioning may have failed"
fi

echo "  Warming up (${WARMUP}s)..."
$COMPOSE --profile bench exec -T hey /hey -z "${WARMUP}s" -c 10 $EXTRA_HEADERS "$TARGET" >/dev/null 2>&1 || true

# Start stats collection in background
STATS_FILE="$RESULTS_DIR/${GATEWAY}-${SCENARIO}-stats.csv"
"$SCRIPT_DIR/collect-stats.sh" "$GATEWAY" "$STATS_FILE" &
STATS_PID=$!

# Build hey command
HEY_ARGS="-z ${DURATION}s -c ${CONCURRENCY}"
if [[ "$QPS" -gt 0 ]]; then
  HEY_ARGS="$HEY_ARGS -q $QPS"
fi

echo "  Running hey for ${DURATION}s (c=${CONCURRENCY}, qps=${QPS:-unlimited})..."
HEY_OUTPUT="$RESULTS_DIR/${GATEWAY}-${SCENARIO}-hey.txt"
$COMPOSE --profile bench exec -T hey /hey $HEY_ARGS $EXTRA_HEADERS "$TARGET" > "$HEY_OUTPUT" 2>&1 || true

# Stop stats collection
kill "$STATS_PID" 2>/dev/null || true
wait "$STATS_PID" 2>/dev/null || true

echo "  Results saved: ${GATEWAY}-${SCENARIO}-hey.txt"

# Stop the gateway (but leave backend and hey running)
cleanup_gateway
