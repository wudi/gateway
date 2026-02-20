//go:build !linux

package loadshed

// readCPUUsage returns 0 on non-Linux platforms.
func readCPUUsage() float64 {
	return 0
}
