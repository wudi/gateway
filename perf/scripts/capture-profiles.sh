#!/usr/bin/env bash
set -euo pipefail

OUTPUT_DIR="${1:-./profiles}"
CPU_SECONDS="${2:-30}"
ADMIN_URL="${ADMIN_URL:-http://localhost:8081}"

mkdir -p "$OUTPUT_DIR"

echo "Capturing profiles to $OUTPUT_DIR (CPU: ${CPU_SECONDS}s)..."

# Capture CPU profile (blocking)
echo "  CPU profile (${CPU_SECONDS}s)..."
curl -sf "${ADMIN_URL}/debug/pprof/profile?seconds=${CPU_SECONDS}" \
    -o "$OUTPUT_DIR/cpu.prof" 2>/dev/null && echo "    OK" || echo "    SKIPPED (pprof not enabled?)"

# Capture point-in-time profiles (parallel)
for profile in heap goroutine allocs mutex block threadcreate; do
    echo "  ${profile}..."
    curl -sf "${ADMIN_URL}/debug/pprof/${profile}" \
        -o "$OUTPUT_DIR/${profile}.prof" 2>/dev/null && echo "    OK" || echo "    SKIPPED"
done

echo "Done. Profiles saved to $OUTPUT_DIR"
echo ""
echo "Analyze with:"
echo "  go tool pprof $OUTPUT_DIR/cpu.prof"
echo "  go tool pprof $OUTPUT_DIR/heap.prof"
echo "  go tool pprof -http=:8090 $OUTPUT_DIR/cpu.prof"
