//go:build linux

package loadshed

import (
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	prevIdle  uint64
	prevTotal uint64
)

// readCPUUsage reads CPU usage from /proc/stat on Linux.
// Returns a percentage 0-100.
func readCPUUsage() float64 {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return 0
	}

	// First line: "cpu  user nice system idle iowait irq softirq steal guest guest_nice"
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0
	}

	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		val, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			continue
		}
		total += val
		if i == 4 { // idle field
			idle = val
		}
	}

	// On first call, store and return 0
	if prevTotal == 0 {
		prevIdle = idle
		prevTotal = total
		return 0
	}

	totalDelta := total - prevTotal
	idleDelta := idle - prevIdle
	prevIdle = idle
	prevTotal = total

	if totalDelta == 0 {
		return 0
	}

	return float64(totalDelta-idleDelta) / float64(totalDelta) * 100
}

// For the first sample, give the CPU reading a moment
func init() {
	_ = readCPUUsage()
	time.Sleep(10 * time.Millisecond)
}
