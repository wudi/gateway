#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PERF_DIR="$(dirname "$SCRIPT_DIR")"
PROJECT_DIR="$(dirname "$PERF_DIR")"
RESULTS_DIR="$PERF_DIR/results/$(date +%Y%m%d-%H%M%S)"
TEST_TYPE="${1:-smoke}"
COMPOSE_FILE="$PERF_DIR/docker-compose.perf.yaml"

mkdir -p "$RESULTS_DIR"

echo "=== Gateway Performance Test ==="
echo "Test type: $TEST_TYPE"
echo "Results:   $RESULTS_DIR"
echo ""

# Step 1: Build runway
echo ">>> Building runway Docker image..."
(cd "$PROJECT_DIR" && make docker-build)

# Step 2: Start infrastructure
echo ">>> Starting perf stack..."
docker compose -f "$COMPOSE_FILE" --profile monitoring up -d
echo ">>> Waiting for services to be healthy..."
sleep 10

# Wait for runway health
for i in $(seq 1 30); do
    if curl -sf http://localhost:8081/health > /dev/null 2>&1; then
        echo ">>> Gateway is healthy"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "ERROR: Gateway did not become healthy within 30s"
        docker compose -f "$COMPOSE_FILE" --profile monitoring logs runway
        exit 1
    fi
    sleep 1
done

# Step 3: Capture baseline profiles
echo ">>> Capturing baseline profiles..."
"$SCRIPT_DIR/capture-profiles.sh" "$RESULTS_DIR/baseline" 5

# Step 4: Run k6 test
echo ">>> Running k6 $TEST_TYPE test..."
docker compose -f "$COMPOSE_FILE" --profile k6 run --rm \
    -e K6_OUT=experimental-prometheus-rw \
    k6 run "/scripts/${TEST_TYPE}.js" \
    --summary-export "/results/summary-${TEST_TYPE}.json" \
    2>&1 | tee "$RESULTS_DIR/k6-output.txt"

# Step 5: Capture post-test profiles
echo ">>> Capturing post-test profiles..."
"$SCRIPT_DIR/capture-profiles.sh" "$RESULTS_DIR/post-test" 5

echo ""
echo "=== Test Complete ==="
echo "Results saved to: $RESULTS_DIR"
echo "Grafana dashboard: http://localhost:3000/d/runway-perf"
echo ""
echo "To stop the stack: make perf-stack-down"
