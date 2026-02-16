#!/usr/bin/env bash
set -euo pipefail

# Parse hey output and stats CSVs into a markdown comparison report.
# Usage: report.sh <results_dir> <scenarios>

RESULTS_DIR="$1"
SCENARIOS="${2:-simple}"

REPORT="$RESULTS_DIR/REPORT.md"

# Parse hey output file for a metric
parse_hey() {
  local file="$1"
  local metric="$2"

  if [[ ! -f "$file" ]]; then
    echo "N/A"
    return
  fi

  case "$metric" in
    rps)
      grep 'Requests/sec:' "$file" | awk '{printf "%.0f", $2}' || echo "N/A"
      ;;
    avg)
      grep 'Average:' "$file" | head -1 | awk '{printf "%.2f", $2 * 1000}' || echo "N/A"
      ;;
    p50)
      # hey shows percentile distribution; 50% line
      awk '/^  50% in/ {printf "%.2f", $3 * 1000}' "$file" || echo "N/A"
      ;;
    p95)
      awk '/^  95% in/ {printf "%.2f", $3 * 1000}' "$file" || echo "N/A"
      ;;
    p99)
      awk '/^  99% in/ {printf "%.2f", $3 * 1000}' "$file" || echo "N/A"
      ;;
    total)
      # Total requests from the summary line
      awk '/^  Total:/ {printf "%.0f", $2}' "$file" | head -1 || echo "N/A"
      ;;
    errors)
      # Count non-200 responses
      local err_line
      err_line=$(grep '^\[' "$file" 2>/dev/null | grep -v '^\[200\]' | awk '{sum += $2} END {printf "%d", sum}')
      echo "${err_line:-0}"
      ;;
  esac
}

# Parse stats CSV for resource usage
parse_stats() {
  local file="$1"
  local metric="$2"

  if [[ ! -f "$file" || $(wc -l < "$file") -le 1 ]]; then
    echo "N/A"
    return
  fi

  case "$metric" in
    avg_cpu)
      tail -n +2 "$file" | awk -F',' '{sum += $2; n++} END {if(n>0) printf "%.1f", sum/n; else print "N/A"}'
      ;;
    peak_mem)
      tail -n +2 "$file" | awk -F',' 'BEGIN{max=0} {if($3+0 > max) max=$3+0} END {if(max>0) printf "%.1f", max; else print "N/A"}'
      ;;
  esac
}

# Gateway display names
declare -A GW_NAMES=(
  [gw]="Gateway (this)"
  [kong]="Kong 3.9"
  [krakend]="KrakenD 2.7"
  [traefik]="Traefik 3.3"
  [tyk]="Tyk 5.7"
)

cat > "$REPORT" <<'HEADER'
# Gateway Benchmark Comparison

HEADER

echo "Generated: $(date -u '+%Y-%m-%d %H:%M:%S UTC')" >> "$REPORT"
echo "" >> "$REPORT"

# System info
echo "## Environment" >> "$REPORT"
echo "" >> "$REPORT"
echo "- **Host:** $(uname -n)" >> "$REPORT"
echo "- **OS:** $(uname -sr)" >> "$REPORT"
echo "- **CPUs:** $(nproc)" >> "$REPORT"
echo "- **Memory:** $(free -h 2>/dev/null | awk '/^Mem:/{print $2}' || echo 'unknown')" >> "$REPORT"
echo "- **Docker:** $(docker version --format '{{.Server.Version}}' 2>/dev/null || echo 'unknown')" | tr -d '\n' >> "$REPORT"
echo "" >> "$REPORT"
echo "" >> "$REPORT"

for scenario in $SCENARIOS; do
  echo "## Scenario: $scenario" >> "$REPORT"
  echo "" >> "$REPORT"
  echo "| Gateway | RPS | Avg (ms) | P50 (ms) | P95 (ms) | P99 (ms) | Avg CPU% | Peak Mem (MB) |" >> "$REPORT"
  echo "|---------|-----|----------|----------|----------|----------|----------|---------------|" >> "$REPORT"

  for gw in gw kong krakend traefik tyk; do
    HEY_FILE="$RESULTS_DIR/${gw}-${scenario}-hey.txt"
    STATS_FILE="$RESULTS_DIR/${gw}-${scenario}-stats.csv"

    if [[ ! -f "$HEY_FILE" ]]; then
      continue
    fi

    NAME="${GW_NAMES[$gw]:-$gw}"
    RPS=$(parse_hey "$HEY_FILE" rps)
    AVG=$(parse_hey "$HEY_FILE" avg)
    P50=$(parse_hey "$HEY_FILE" p50)
    P95=$(parse_hey "$HEY_FILE" p95)
    P99=$(parse_hey "$HEY_FILE" p99)
    AVG_CPU=$(parse_stats "$STATS_FILE" avg_cpu)
    PEAK_MEM=$(parse_stats "$STATS_FILE" peak_mem)

    echo "| $NAME | $RPS | $AVG | $P50 | $P95 | $P99 | $AVG_CPU | $PEAK_MEM |" >> "$REPORT"
  done

  echo "" >> "$REPORT"
done

# Caveats
cat >> "$REPORT" <<'FOOTER'
## Caveats

- All gateways run as Docker containers with default resource limits.
- KrakenD and Traefik do not have native API key auth, so the `auth` scenario only tests Kong, Tyk, and this gateway.
- Tyk requires Redis, which adds a dependency not needed by other gateways.
- Rate limit implementations differ across gateways (sliding window, token bucket, fixed window). The `ratelimit` scenario tests whether the gateway can handle the configured limit without rejecting valid traffic.
- Results are from a single machine. Production performance varies with hardware, network, and configuration tuning.
- `hey` runs inside Docker on the same host, so network overhead is minimal but CPU is shared.
FOOTER

echo "Report written to $REPORT"
