package loadshed

import (
	"net/http"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/wudi/gateway/internal/config"
	"github.com/wudi/gateway/internal/middleware"
)

// LoadShedder monitors system resources and rejects requests when thresholds are exceeded.
type LoadShedder struct {
	cfg          config.LoadSheddingConfig
	shedding     atomic.Bool
	cooldownEnd  atomic.Int64 // unix nano

	// Stats
	rejected atomic.Int64
	allowed  atomic.Int64

	// Current readings
	cpuPercent     atomic.Int64 // stored as percent * 100 (fixed point)
	memoryPercent  atomic.Int64 // stored as percent * 100
	goroutineCount atomic.Int64

	stopCh chan struct{}
}

// New creates a new LoadShedder that starts background sampling.
func New(cfg config.LoadSheddingConfig) *LoadShedder {
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = time.Second
	}
	if cfg.RetryAfter <= 0 {
		cfg.RetryAfter = 5
	}
	if cfg.CooldownDuration <= 0 {
		cfg.CooldownDuration = 5 * time.Second
	}
	if cfg.CPUThreshold <= 0 {
		cfg.CPUThreshold = 90
	}
	if cfg.MemoryThreshold <= 0 {
		cfg.MemoryThreshold = 85
	}

	ls := &LoadShedder{
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}

	go ls.sampleLoop()
	return ls
}

func (ls *LoadShedder) sampleLoop() {
	ticker := time.NewTicker(ls.cfg.SampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ls.stopCh:
			return
		case <-ticker.C:
			ls.sample()
		}
	}
}

func (ls *LoadShedder) sample() {
	// Read memory
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	memPct := float64(0)
	if memStats.Sys > 0 {
		memPct = float64(memStats.HeapAlloc) / float64(memStats.Sys) * 100
	}
	ls.memoryPercent.Store(int64(memPct * 100))

	// Read goroutines
	goroutines := runtime.NumGoroutine()
	ls.goroutineCount.Store(int64(goroutines))

	// Read CPU (platform-specific)
	cpuPct := readCPUUsage()
	ls.cpuPercent.Store(int64(cpuPct * 100))

	// Check thresholds
	exceeded := false
	if ls.cfg.CPUThreshold > 0 && cpuPct > ls.cfg.CPUThreshold {
		exceeded = true
	}
	if ls.cfg.MemoryThreshold > 0 && memPct > ls.cfg.MemoryThreshold {
		exceeded = true
	}
	if ls.cfg.GoroutineLimit > 0 && goroutines > ls.cfg.GoroutineLimit {
		exceeded = true
	}

	if exceeded {
		ls.shedding.Store(true)
		ls.cooldownEnd.Store(time.Now().Add(ls.cfg.CooldownDuration).UnixNano())
	} else if ls.shedding.Load() && time.Now().UnixNano() >= ls.cooldownEnd.Load() {
		ls.shedding.Store(false)
	}
}

// Middleware returns a middleware that rejects requests when shedding is active.
func (ls *LoadShedder) Middleware() middleware.Middleware {
	retryAfter := strconv.Itoa(ls.cfg.RetryAfter)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ls.shedding.Load() {
				ls.rejected.Add(1)
				w.Header().Set("Retry-After", retryAfter)
				http.Error(w, `{"error":"service overloaded"}`, http.StatusServiceUnavailable)
				return
			}
			ls.allowed.Add(1)
			next.ServeHTTP(w, r)
		})
	}
}

// Close stops the background sampling goroutine.
func (ls *LoadShedder) Close() {
	close(ls.stopCh)
}

// Stats returns current load shedding statistics.
func (ls *LoadShedder) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enabled":         ls.cfg.Enabled,
		"shedding":        ls.shedding.Load(),
		"rejected":        ls.rejected.Load(),
		"allowed":         ls.allowed.Load(),
		"cpu_percent":     float64(ls.cpuPercent.Load()) / 100,
		"memory_percent":  float64(ls.memoryPercent.Load()) / 100,
		"goroutine_count": ls.goroutineCount.Load(),
	}
}
