#!/usr/bin/env bash
set -euo pipefail

# Collect docker stats for a container at 1-second intervals.
# Usage: collect-stats.sh <service_name> <output_csv>

SERVICE="$1"
OUTPUT="$2"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPARE_DIR="$(dirname "$SCRIPT_DIR")"

# Find the container ID for the service
CONTAINER=$( docker compose -f "$COMPARE_DIR/docker-compose.compare.yaml" ps -q "$SERVICE" 2>/dev/null | head -1 )

if [[ -z "$CONTAINER" ]]; then
  echo "timestamp,cpu_percent,mem_usage_mb,mem_limit_mb" > "$OUTPUT"
  exit 0
fi

echo "timestamp,cpu_percent,mem_usage_mb,mem_limit_mb" > "$OUTPUT"

while true; do
  STATS=$(docker stats --no-stream --format '{{.CPUPerc}},{{.MemUsage}}' "$CONTAINER" 2>/dev/null) || break

  # Parse: "1.23%,45.6MiB / 1.234GiB"
  CPU=$(echo "$STATS" | cut -d',' -f1 | tr -d '%')
  MEM_PART=$(echo "$STATS" | cut -d',' -f2)
  MEM_USAGE=$(echo "$MEM_PART" | cut -d'/' -f1 | xargs)
  MEM_LIMIT=$(echo "$MEM_PART" | cut -d'/' -f2 | xargs)

  # Convert to MB
  to_mb() {
    local val="$1"
    if echo "$val" | grep -q 'GiB'; then
      echo "$val" | tr -d 'GiB ' | awk '{printf "%.1f", $1 * 1024}'
    elif echo "$val" | grep -q 'MiB'; then
      echo "$val" | tr -d 'MiB '
    elif echo "$val" | grep -q 'KiB'; then
      echo "$val" | tr -d 'KiB ' | awk '{printf "%.1f", $1 / 1024}'
    else
      echo "0"
    fi
  }

  MEM_MB=$(to_mb "$MEM_USAGE")
  LIM_MB=$(to_mb "$MEM_LIMIT")
  TS=$(date +%s)

  echo "$TS,$CPU,$MEM_MB,$LIM_MB" >> "$OUTPUT"
  sleep 1
done
